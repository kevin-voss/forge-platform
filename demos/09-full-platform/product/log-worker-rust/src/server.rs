use crate::config::Config;
use crate::events::EventsStatus;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};
use std::time::Instant;

#[derive(Clone)]
pub struct AppState {
    pub cfg: Config,
    pub started_at: Instant,
    pub entries: Arc<Mutex<Vec<LogEntry>>>,
    pub events_status: Arc<Mutex<EventsStatus>>,
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

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct LogEntry {
    pub source: String,
    pub level: String,
    pub message: String,
}

#[derive(Debug, Serialize)]
struct LogListResponse {
    items: Vec<LogEntry>,
    processed: usize,
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/health/live", get(handle_live))
        .route("/health/ready", get(handle_ready))
        .route("/", get(handle_identity))
        .route("/logs", post(handle_ingest).get(handle_list))
        .route("/events/status", get(handle_events_status))
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

async fn handle_identity(State(state): State<Arc<AppState>>) -> impl IntoResponse {
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

async fn handle_ingest(
    State(state): State<Arc<AppState>>,
    Json(entry): Json<LogEntry>,
) -> impl IntoResponse {
    if entry.message.trim().is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(serde_json::json!({"error": "message_required"})),
        )
            .into_response();
    }
    let mut entries = state.entries.lock().expect("log mutex");
    // Normalize level for later product assertions.
    let mut normalized = entry;
    if normalized.level.trim().is_empty() {
        normalized.level = "info".into();
    }
    if normalized.source.trim().is_empty() {
        normalized.source = "unknown".into();
    }
    entries.push(normalized.clone());
    (
        StatusCode::ACCEPTED,
        Json(serde_json::json!({
            "status": "accepted",
            "processed": entries.len(),
            "entry": normalized,
        })),
    )
        .into_response()
}

async fn handle_list(State(state): State<Arc<AppState>>) -> impl IntoResponse {
    let entries = state.entries.lock().expect("log mutex");
    (
        StatusCode::OK,
        Json(LogListResponse {
            processed: entries.len(),
            items: entries.clone(),
        }),
    )
}

async fn handle_events_status(State(state): State<Arc<AppState>>) -> impl IntoResponse {
    let status = state.events_status.lock().expect("events status mutex");
    (
        StatusCode::OK,
        Json(serde_json::json!({
            "ready": status.ready,
            "processed_count": status.processed_count,
            "last_incident_id": status.last_incident_id,
            "last_error": status.last_error,
            "subject": state.cfg.events_subject,
            "consumer": state.cfg.events_consumer,
            "events_url_configured": !state.cfg.events_url.is_empty(),
        })),
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
                service_name: "incident-log-worker".into(),
                service_version: "0.1.0".into(),
                log_level: "info".into(),
                env: "development".into(),
                events_url: String::new(),
                events_consumer: "incident-log-worker".into(),
                events_subject: "incident.created".into(),
                events_poll_ms: 500,
            },
            started_at: Instant::now()
                .checked_sub(std::time::Duration::from_secs(2))
                .unwrap_or_else(Instant::now),
            entries: Arc::new(Mutex::new(Vec::new())),
            events_status: Arc::new(Mutex::new(EventsStatus::default())),
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
        assert_eq!(body["service"], "incident-log-worker");
        assert_eq!(body["language"], "rust");
        assert!(body["uptime_seconds"].as_f64().unwrap() > 0.0);
    }

    #[tokio::test]
    async fn ingest_and_list_logs() {
        let state = test_state();
        let app = router(state.clone());
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .method("POST")
                    .uri("/logs")
                    .header("content-type", "application/json")
                    .body(axum::body::Body::from(
                        r#"{"source":"api","level":"error","message":"boom"}"#,
                    ))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::ACCEPTED);

        let app = router(state);
        let (status, body) = get_json(app, "/logs").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["processed"], 1);
        assert_eq!(body["items"][0]["message"], "boom");
    }
}

