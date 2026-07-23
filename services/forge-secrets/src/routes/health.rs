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

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
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

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::BodyExt;
    use std::sync::atomic::{AtomicBool, AtomicU64};
    use std::sync::Arc;
    use std::time::Instant;
    use tower::ServiceExt;

    fn test_state(ready: bool) -> AppState {
        AppState {
            service_name: "forge-secrets".into(),
            service_version: "0.1.0".into(),
            started_at: Instant::now(),
            pool: None,
            key_provider: None,
            master_key_id: "m1".into(),
            aead_alg: crate::crypto::AeadAlg::Aes256Gcm,
            max_value_bytes: 65536,
            ready: Arc::new(AtomicBool::new(ready)),
            data_keys_total: Arc::new(AtomicU64::new(0)),
            secrets_total: Arc::new(AtomicU64::new(0)),
            secret_access_total: Arc::new(AtomicU64::new(0)),
            secret_resolves_total: Arc::new(AtomicU64::new(0)),
            config_values_total: Arc::new(AtomicU64::new(0)),
            crypto_ok: ready,
            crypto_error: if ready {
                None
            } else {
                Some("missing key".into())
            },
            auth_mode: "dev".into(),
            identity: None,
            auth_metrics: crate::auth::middleware::AuthMetrics::new(),
            audit_enabled: true,
            audit_strict: false,
            audit_metrics: crate::audit::recorder::AuditMetrics::new(),
            log_masking_enabled: true,
            mask_placeholder: "***".into(),
            known_secrets: std::sync::Arc::new(crate::masking::KnownSecrets::new()),
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
    async fn ready_503_without_crypto_or_db() {
        let app = router().with_state(test_state(false));
        let (status, body) = get_json(app, "/health/ready").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["status"], "not_ready");
    }
}
