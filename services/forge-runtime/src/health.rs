use crate::docker::DockerEngine;
use crate::heartbeat::Heartbeat;
use crate::node::Node;
use crate::prober::Prober;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use std::sync::Arc;
use std::time::Duration;

#[derive(Clone)]
pub struct AppState {
    pub docker: Arc<dyn DockerEngine>,
    pub node: Arc<Node>,
    pub heartbeat: Arc<Heartbeat>,
    pub pull_timeout: Duration,
    pub prober: Arc<Prober>,
    pub log_default_tail: u32,
    pub log_stream_buffer: usize,
}

#[derive(Debug, Serialize, PartialEq)]
pub struct HealthResponse {
    pub status: String,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
}

async fn handle_live() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(HealthResponse {
            status: "ok".into(),
        }),
    )
}

async fn handle_ready(State(state): State<AppState>) -> impl IntoResponse {
    match state.docker.ping().await {
        Ok(()) => (
            StatusCode::OK,
            Json(HealthResponse {
                status: "ok".into(),
            }),
        ),
        Err(_) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(HealthResponse {
                status: "not_ready".into(),
            }),
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::StubDocker;
    use crate::docker::DockerProbe;
    use crate::heartbeat::Heartbeat;
    use crate::node::Node;
    use http_body_util::BodyExt;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_state(docker: Arc<dyn DockerEngine>) -> AppState {
        use crate::prober::{ProbeConfig, Prober, StatusCache};

        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), docker.as_ref())
            .await
            .expect("node");
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker),
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .expect("prober"),
        );
        // Node id is in-memory; tempdir may drop.
        AppState {
            docker,
            node: Arc::new(node),
            heartbeat: Arc::new(Heartbeat::new()),
            pull_timeout: Duration::from_secs(30),
            prober,
            log_default_tail: 100,
            log_stream_buffer: 8192,
        }
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
    async fn live_always_ok() {
        let state = test_state(Arc::new(StubDocker::down())).await;
        let app = router().with_state(state);
        let (status, body) = get_json(app, "/health/live").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }

    #[tokio::test]
    async fn ready_ok_when_docker_reachable() {
        let state = test_state(Arc::new(StubDocker::ok("1.0.0"))).await;
        let app = router().with_state(state);
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }

    #[tokio::test]
    async fn ready_503_when_docker_unreachable() {
        let state = test_state(Arc::new(StubDocker::down())).await;
        let app = router().with_state(state);
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "not_ready");
    }

    #[tokio::test]
    async fn ready_503_with_missing_unix_socket() {
        use crate::docker::BollardDocker;

        let docker = BollardDocker::connect("unix:///tmp/forge-runtime-missing.sock");
        let state = test_state(Arc::new(docker)).await;
        let app = router().with_state(state);
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "not_ready");
    }

    #[tokio::test]
    async fn ready_200_with_local_docker_when_available() {
        use crate::docker::BollardDocker;

        let docker = BollardDocker::connect("unix:///var/run/docker.sock");
        if docker.ping().await.is_err() {
            return;
        }
        let state = test_state(Arc::new(docker)).await;
        let app = router().with_state(state);
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }
}
