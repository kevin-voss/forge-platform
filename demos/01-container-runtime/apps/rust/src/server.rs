use crate::config::Config;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use std::sync::Arc;
use std::time::Instant;

#[derive(Clone)]
pub struct AppState {
    pub cfg: Config,
    pub started_at: Instant,
}

#[derive(Debug, Serialize, PartialEq)]
pub struct HealthResponse {
    pub status: String,
}

#[derive(Debug, Serialize, PartialEq)]
pub struct IdentityResponse {
    pub service: String,
    pub language: String,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub uptime_seconds: Option<f64>,
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
        .route("/", get(handle_identity))
        .with_state(Arc::new(state))
}

async fn handle_live() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(HealthResponse {
            status: "ok".into(),
        }),
    )
}

async fn handle_ready() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(HealthResponse {
            status: "ok".into(),
        }),
    )
}

async fn handle_identity(
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> impl IntoResponse {
    let uptime = state.started_at.elapsed().as_secs_f64();
    (
        StatusCode::OK,
        Json(IdentityResponse {
            service: state.cfg.service_name.clone(),
            language: "rust".into(),
            status: "running".into(),
            version: Some(state.cfg.service_version.clone()),
            uptime_seconds: Some(uptime),
        }),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    fn test_state() -> AppState {
        AppState {
            cfg: Config {
                port: 8080,
                service_name: "demo-rust-api".into(),
                service_version: "0.1.0".into(),
                log_level: "info".into(),
                env: "development".into(),
            },
            started_at: Instant::now()
                .checked_sub(std::time::Duration::from_secs(2))
                .unwrap_or_else(Instant::now),
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
    async fn health_endpoints() {
        for path in ["/health/live", "/health/ready"] {
            let app = router(test_state());
            let (status, body) = get_json(app, path).await;
            assert_eq!(status, StatusCode::OK);
            assert_eq!(body["status"], "ok");
        }
    }

    #[tokio::test]
    async fn identity_endpoint() {
        let app = router(test_state());
        let (status, body) = get_json(app, "/").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["service"], "demo-rust-api");
        assert_eq!(body["language"], "rust");
        assert_eq!(body["status"], "running");
        assert_eq!(body["version"], "0.1.0");
        assert!(body["uptime_seconds"].as_f64().unwrap() > 0.0);
    }
}
