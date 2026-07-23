use crate::state::AppState;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;

#[derive(Debug, Serialize, PartialEq)]
pub struct HealthResponse {
    pub status: String,
}

#[derive(Debug, Serialize)]
pub struct IdentityResponse {
    pub service: String,
    pub language: String,
    pub status: String,
    pub version: String,
    pub uptime_seconds: f64,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
        .route("/", get(handle_identity))
}

async fn handle_live() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(HealthResponse {
            status: "live".into(),
        }),
    )
}

async fn handle_ready(State(state): State<AppState>) -> impl IntoResponse {
    state.refresh_ready().await;
    if state.is_ready() {
        (
            StatusCode::OK,
            Json(HealthResponse {
                status: "ready".into(),
            }),
        )
    } else {
        (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(HealthResponse {
                status: "not_ready".into(),
            }),
        )
    }
}

async fn handle_identity(State(state): State<AppState>) -> Json<IdentityResponse> {
    Json(IdentityResponse {
        service: state.service_name.clone(),
        language: "rust".into(),
        status: "running".into(),
        version: state.service_version.clone(),
        uptime_seconds: state.started_at.elapsed().as_secs_f64(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::LocalStore;
    use http_body_util::BodyExt;
    use std::sync::atomic::AtomicBool;
    use std::sync::Arc;
    use std::time::Instant;
    use tower::ServiceExt;

    fn test_state(ready: bool) -> AppState {
        let root = std::path::PathBuf::from(format!(
            "/tmp/forge-memory-unit-{}-{}",
            std::process::id(),
            ready
        ));
        let store = Arc::new(LocalStore::new(&root, "/tmp"));
        AppState {
            service_name: "forge-memory".into(),
            service_version: "0.1.0".into(),
            started_at: Instant::now(),
            store,
            ready: Arc::new(AtomicBool::new(ready)),
            collections: Arc::new(std::sync::Mutex::new(None)),
            metrics: Arc::new(crate::state::MemoryMetrics::default()),
            list_page_size: 100,
            max_dim: 4096,
            max_metadata_bytes: 65_536,
            meta_path: root.join("meta/index.db"),
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
        let app = router().with_state(test_state(false));
        let (status, body) = get_json(app, "/health/live").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "live");
    }

    #[tokio::test]
    async fn ready_503_when_not_ready() {
        let app = router().with_state(test_state(false));
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "not_ready");
    }

    #[tokio::test]
    async fn identity_contains_required_fields() {
        let app = router().with_state(test_state(true));
        let (status, body) = get_json(app, "/").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["service"], "forge-memory");
        assert_eq!(body["language"], "rust");
        assert_eq!(body["status"], "running");
        assert_eq!(body["version"], "0.1.0");
        assert!(body["uptime_seconds"].as_f64().unwrap() >= 0.0);
    }
}
