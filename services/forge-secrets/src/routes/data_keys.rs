use crate::state::{AppState, EnsureError};
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::post;
use axum::{Json, Router};
use serde::Serialize;

#[derive(Debug, Serialize)]
pub struct DataKeyMetaResponse {
    pub project_id: String,
    pub key_version: i32,
    pub master_key_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub created: Option<bool>,
    /// Always false — plaintext is never returned.
    pub plaintext_returned: bool,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
}

pub fn router() -> Router<AppState> {
    Router::new().route(
        "/v1/projects/{project_id}/data-keys",
        post(ensure_data_key).get(verify_data_key),
    )
}

async fn ensure_data_key(
    State(state): State<AppState>,
    Path(project_id): Path<String>,
) -> impl IntoResponse {
    if project_id.trim().is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: "project_id required".into(),
            }),
        )
            .into_response();
    }
    match state.ensure_project_data_key(&project_id).await {
        Ok((row, created)) => {
            let status = if created {
                StatusCode::CREATED
            } else {
                StatusCode::OK
            };
            (
                status,
                Json(DataKeyMetaResponse {
                    project_id: row.project_id,
                    key_version: row.key_version,
                    master_key_id: row.master_key_id,
                    created: Some(created),
                    plaintext_returned: false,
                }),
            )
                .into_response()
        }
        Err(EnsureError::NotReady) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
            }),
        )
            .into_response(),
        Err(EnsureError::NotFound) => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "not found".into(),
            }),
        )
            .into_response(),
        Err(EnsureError::Storage(err)) | Err(EnsureError::Crypto(err)) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(ErrorBody { error: err }),
        )
            .into_response(),
    }
}

async fn verify_data_key(
    State(state): State<AppState>,
    Path(project_id): Path<String>,
) -> impl IntoResponse {
    match state.verify_project_data_key(&project_id).await {
        Ok(row) => (
            StatusCode::OK,
            Json(DataKeyMetaResponse {
                project_id: row.project_id,
                key_version: row.key_version,
                master_key_id: row.master_key_id,
                created: None,
                plaintext_returned: false,
            }),
        )
            .into_response(),
        Err(EnsureError::NotReady) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
            }),
        )
            .into_response(),
        Err(EnsureError::NotFound) => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "data key not found".into(),
            }),
        )
            .into_response(),
        Err(EnsureError::Storage(err)) | Err(EnsureError::Crypto(err)) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(ErrorBody { error: err }),
        )
            .into_response(),
    }
}
