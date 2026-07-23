//! `GET /v1/usage` — per-project bytes used vs quota (13.06).

use crate::meta::MetaError;
use crate::project::ProjectContext;
use crate::state::AppState;
use axum::extract::{Extension, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use tracing::warn;

#[derive(Debug, Serialize)]
pub struct UsageResponse {
    pub project_id: String,
    pub used_bytes: i64,
    pub quota_bytes: i64,
    pub objects: i64,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/usage", get(get_usage))
}

async fn get_usage(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
) -> Response {
    let Some(meta) = state.meta.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "metadata store unavailable".into(),
                code: Some("unavailable"),
            }),
        )
            .into_response();
    };

    match meta.project_usage_report(&project.project_id, state.default_quota_bytes) {
        Ok(report) => (
            StatusCode::OK,
            Json(UsageResponse {
                project_id: report.project_id,
                used_bytes: report.used_bytes,
                quota_bytes: report.quota_bytes,
                objects: report.objects,
            }),
        )
            .into_response(),
        Err(MetaError::Invalid(msg)) => (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: msg,
                code: Some("invalid"),
            }),
        )
            .into_response(),
        Err(MetaError::Internal(msg)) => {
            warn!(error = %msg, "usage query failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "internal error".into(),
                    code: Some("internal"),
                }),
            )
                .into_response()
        }
        Err(other) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(ErrorBody {
                error: other.to_string(),
                code: Some("internal"),
            }),
        )
            .into_response(),
    }
}
