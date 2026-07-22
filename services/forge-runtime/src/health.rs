use crate::docker::DockerProbe;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub docker: Arc<dyn DockerProbe>,
}

#[derive(Debug, Serialize, PartialEq)]
pub struct HealthResponse {
    pub status: String,
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
        .with_state(state)
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
    use http_body_util::BodyExt;
    use tower::ServiceExt;

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
        let app = router(AppState {
            docker: Arc::new(StubDocker::down()),
        });
        let (status, body) = get_json(app, "/health/live").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }

    #[tokio::test]
    async fn ready_ok_when_docker_reachable() {
        let app = router(AppState {
            docker: Arc::new(StubDocker::ok("1.0.0")),
        });
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }

    #[tokio::test]
    async fn ready_503_when_docker_unreachable() {
        let app = router(AppState {
            docker: Arc::new(StubDocker::down()),
        });
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "not_ready");
    }

    #[tokio::test]
    async fn ready_503_with_missing_unix_socket() {
        use crate::docker::BollardDocker;

        let docker = BollardDocker::connect("unix:///tmp/forge-runtime-missing.sock");
        let app = router(AppState {
            docker: Arc::new(docker),
        });
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
        let app = router(AppState {
            docker: Arc::new(docker),
        });
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "ok");
    }
}
