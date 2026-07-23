use crate::health::AppState;
use crate::status::WorkloadStatus;
use crate::workload::env::SECRETS_FINGERPRINT_LABEL;
use crate::workload::{DEPLOYMENT_ID_LABEL, MANAGED_LABEL, MANAGED_LABEL_VALUE};
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;

/// Actual node state exposed for Control pull (`GET /v1/node/state`).
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NodeStateResponse {
    pub node_id: String,
    pub workloads: Vec<NodeWorkloadState>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NodeWorkloadState {
    pub deployment_id: String,
    pub status: WorkloadStatus,
    pub host_port: u16,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub image: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub secrets_fingerprint: Option<String>,
}

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/node/state", get(handle_node_state))
}

async fn handle_node_state(State(state): State<AppState>) -> impl IntoResponse {
    match build_state(&state).await {
        Ok(body) => (StatusCode::OK, Json(body)).into_response(),
        Err(err) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(serde_json::json!({
                "error": {"code": "node_state_failed", "message": err}
            })),
        )
            .into_response(),
    }
}

async fn build_state(state: &AppState) -> Result<NodeStateResponse, String> {
    let listed = state.docker.list_managed_containers().await?;
    let mut workloads = Vec::new();

    for inspect in listed {
        let labels = match &inspect.labels {
            Some(l) => l,
            None => continue,
        };
        if labels.get(MANAGED_LABEL).map(String::as_str) != Some(MANAGED_LABEL_VALUE) {
            continue;
        }
        let Some(deployment_id) = labels.get(DEPLOYMENT_ID_LABEL).cloned() else {
            continue;
        };
        if deployment_id.is_empty() {
            continue;
        }

        let host_port = inspect
            .port_bindings
            .values()
            .flatten()
            .copied()
            .next()
            .unwrap_or(0);

        let status = match state.prober.status_for(&deployment_id).await {
            Ok(view) => view.status,
            Err(_) => match inspect.state.to_ascii_lowercase().as_str() {
                "running" | "restarting" => WorkloadStatus::Starting,
                "exited" | "dead" => WorkloadStatus::Failed,
                _ => WorkloadStatus::Stopped,
            },
        };

        let secrets_fingerprint = labels
            .get(SECRETS_FINGERPRINT_LABEL)
            .cloned()
            .filter(|s| !s.is_empty());

        workloads.push(NodeWorkloadState {
            deployment_id,
            status,
            host_port,
            image: inspect.image,
            secrets_fingerprint,
        });
    }

    workloads.sort_by(|a, b| a.deployment_id.cmp(&b.deployment_id));

    Ok(NodeStateResponse {
        node_id: state.node.info.id.clone(),
        workloads,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::{RecordingDocker, StubDocker};
    use crate::heartbeat::Heartbeat;
    use crate::lifecycle::DeploymentLocks;
    use crate::node::Node;
    use crate::prober::{ProbeConfig, Prober, StatusCache};
    use crate::workload::{self, WorkloadSpec};
    use http_body_util::BodyExt;
    use std::collections::HashMap;
    use std::sync::Arc;
    use std::time::Duration;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_app(docker: RecordingDocker) -> Router {
        let dir = tempdir().unwrap();
        let node = Arc::new(
            Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
                .await
                .unwrap(),
        );
        let docker: Arc<dyn crate::docker::DockerEngine> = Arc::new(docker);
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker),
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .unwrap(),
        );
        let state = AppState {
            docker,
            node,
            heartbeat: Arc::new(Heartbeat::new()),
            pull_timeout: Duration::from_secs(30),
            prober,
            log_default_tail: 100,
            log_stream_buffer: 8192,
            stop_grace: Duration::from_secs(10),
            on_config_conflict: crate::lifecycle::OnConfigConflict::Recreate,
            deployment_locks: Arc::new(DeploymentLocks::new()),
        };
        router().with_state(state)
    }

    #[tokio::test]
    async fn node_state_lists_managed_workloads() {
        let docker = RecordingDocker::ok(45555);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        workload::create_and_start(
            &docker,
            &node,
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
            },
            Duration::from_secs(5),
        )
        .await
        .unwrap();

        let app = test_app(docker).await;
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/v1/node/state")
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        assert!(json["nodeId"].as_str().unwrap().len() > 0);
        assert_eq!(json["workloads"][0]["deploymentId"], "deployment-123");
        assert_eq!(json["workloads"][0]["hostPort"], 45555);
        assert_eq!(
            json["workloads"][0]["image"],
            "localhost:5000/demo-go:latest"
        );
        assert!(json["workloads"][0]["status"].as_str().is_some());
    }
}
