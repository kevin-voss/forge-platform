use crate::health::AppState;
use crate::heartbeat::Heartbeat;
use crate::node::Node;
use axum::extract::State;
use axum::routing::get;
use axum::{Json, Router};
use chrono::{DateTime, Utc};
use serde::Serialize;

#[derive(Debug, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NodeResponse {
    pub id: String,
    pub hostname: String,
    pub docker_version: String,
    pub cpu: u32,
    pub memory_bytes: u64,
    pub started_at: DateTime<Utc>,
    pub last_heartbeat: DateTime<Utc>,
    /// Network-plane status: `Ready` or `Degraded` (DNS bootstrap failure keeps last resolver).
    pub network_status: String,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/node", get(handle_node))
        .route("/v1/node/heartbeat", get(handle_heartbeat))
}

async fn handle_node(State(state): State<AppState>) -> Json<NodeResponse> {
    Json(node_response(&state.node, &state.heartbeat))
}

async fn handle_heartbeat(State(state): State<AppState>) -> Json<crate::heartbeat::HeartbeatView> {
    Json(state.heartbeat.snapshot(state.node.info.id.clone()))
}

fn node_response(node: &Node, heartbeat: &Heartbeat) -> NodeResponse {
    let info = &node.info;
    NodeResponse {
        id: info.id.clone(),
        hostname: info.hostname.clone(),
        docker_version: info.docker_version.clone(),
        cpu: info.cpu,
        memory_bytes: info.memory_bytes,
        started_at: info.started_at,
        last_heartbeat: heartbeat.last_heartbeat(),
        network_status: node.network_status_label().to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::StubDocker;
    use crate::health::AppState;
    use axum::http::StatusCode;
    use http_body_util::BodyExt;
    use std::sync::Arc;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_app() -> (Router, Arc<Node>, Arc<Heartbeat>) {
        let dir = tempdir().unwrap();
        let docker = StubDocker::ok("29.1.3");
        let node = Arc::new(Node::bootstrap(dir.path(), &docker).await.unwrap());
        // Identity is held in-memory; TempDir may drop after bootstrap.

        let heartbeat = Arc::new(Heartbeat::new());
        heartbeat.tick(Utc::now(), true);
        let docker = Arc::new(docker);
        let prober = Arc::new(
            crate::prober::Prober::new(
                Arc::clone(&docker) as Arc<dyn crate::docker::DockerEngine>,
                Arc::new(crate::prober::StatusCache::new()),
                crate::prober::ProbeConfig::default(),
            )
            .unwrap(),
        );
        let state = AppState {
            docker,
            node: Arc::clone(&node),
            heartbeat: Arc::clone(&heartbeat),
            pull_timeout: std::time::Duration::from_secs(30),
            prober,
            log_default_tail: 100,
            log_stream_buffer: 8192,
            stop_grace: std::time::Duration::from_secs(10),
            on_config_conflict: crate::lifecycle::OnConfigConflict::Recreate,
            deployment_locks: Arc::new(crate::lifecycle::DeploymentLocks::new()),
        };
        (router().with_state(state), node, heartbeat)
    }

    async fn get_json(app: Router, path: &str) -> (StatusCode, serde_json::Value) {
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri(path)
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let status = response.status();
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        (status, json)
    }

    #[tokio::test]
    async fn get_node_returns_identity_and_heartbeat() {
        let (app, node, _) = test_app().await;
        let (status, body) = get_json(app, "/v1/node").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["id"], node.info.id);
        assert_eq!(body["dockerVersion"], "29.1.3");
        assert!(body.get("lastHeartbeat").is_some());
        assert!(body.get("startedAt").is_some());
        assert!(body.get("memoryBytes").is_some());
    }

    #[tokio::test]
    async fn get_heartbeat_reflects_liveness() {
        let (app, node, heartbeat) = test_app().await;
        let t = Utc::now();
        heartbeat.tick(t, true);
        let (status, body) = get_json(app, "/v1/node/heartbeat").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["nodeId"], node.info.id);
        assert_eq!(body["healthy"], true);
        assert!(body.get("at").is_some());
    }

    #[tokio::test]
    async fn node_id_stable_across_rebootstrap() {
        let dir = tempdir().unwrap();
        let docker = StubDocker::ok("1.0.0");
        let n1 = Node::bootstrap(dir.path(), &docker).await.unwrap();
        let n2 = Node::bootstrap(dir.path(), &docker).await.unwrap();
        assert_eq!(n1.info.id, n2.info.id);
    }
}
