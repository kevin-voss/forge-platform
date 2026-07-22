use crate::state::AppState;
use axum::extract::State;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;

#[derive(Debug, Serialize)]
pub struct IdentityResponse {
    pub service: String,
    pub language: String,
    pub status: String,
    pub version: String,
    pub uptime_seconds: f64,
}

pub fn router() -> Router<AppState> {
    Router::new().route("/", get(handle_identity))
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
