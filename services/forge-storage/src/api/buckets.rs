//! Bucket lifecycle REST handlers.

use crate::api::validate::validate_bucket_name;
use crate::meta::{Bucket, MetaError};
use crate::project::ProjectContext;
use crate::state::AppState;
use axum::extract::{Extension, Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use tracing::{info, warn};

#[derive(Debug, Deserialize)]
pub struct CreateBucketRequest {
    pub name: String,
}

#[derive(Debug, Serialize)]
pub struct BucketListResponse {
    pub buckets: Vec<Bucket>,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    object_count: Option<i64>,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/buckets", post(create_bucket).get(list_buckets))
        .route(
            "/v1/buckets/{bucket}",
            get(get_bucket).delete(delete_bucket),
        )
}

fn request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-forge-request-id")
        .or_else(|| headers.get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string()
}

fn meta_err(err: MetaError) -> Response {
    match err {
        MetaError::NotFound => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "bucket not found".into(),
                code: Some("not_found"),
                object_count: None,
            }),
        )
            .into_response(),
        MetaError::Conflict(msg) => {
            let object_count = msg
                .strip_prefix("bucket not empty: ")
                .and_then(|rest| rest.split_whitespace().next())
                .and_then(|n| n.parse().ok());
            (
                StatusCode::CONFLICT,
                Json(ErrorBody {
                    error: msg,
                    code: Some("conflict"),
                    object_count,
                }),
            )
                .into_response()
        }
        MetaError::Invalid(msg) => (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: msg,
                code: Some("invalid"),
                object_count: None,
            }),
        )
            .into_response(),
        MetaError::Internal(msg) => {
            warn!(error = %msg, "metadata store error");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "internal error".into(),
                    code: Some("internal"),
                    object_count: None,
                }),
            )
                .into_response()
        }
    }
}

async fn create_bucket(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Json(body): Json<CreateBucketRequest>,
) -> Response {
    let rid = request_id(&headers);
    if let Err(msg) = validate_bucket_name(&body.name) {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: msg,
                code: Some("invalid_name"),
                object_count: None,
            }),
        )
            .into_response();
    }
    let Some(meta) = state.meta.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "metadata store unavailable".into(),
                code: Some("unavailable"),
                object_count: None,
            }),
        )
            .into_response();
    };
    match meta.create_bucket(&project.project_id, body.name.trim()) {
        Ok(bucket) => {
            state
                .metrics
                .buckets_created
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            info!(
                project_id = %project.project_id,
                bucket = %bucket.name,
                request_id = %rid,
                "bucket created"
            );
            (StatusCode::CREATED, Json(bucket)).into_response()
        }
        Err(err) => meta_err(err),
    }
}

async fn list_buckets(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
) -> Response {
    let Some(meta) = state.meta.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "metadata store unavailable".into(),
                code: Some("unavailable"),
                object_count: None,
            }),
        )
            .into_response();
    };
    match meta.list_buckets(&project.project_id) {
        Ok(buckets) => (StatusCode::OK, Json(BucketListResponse { buckets })).into_response(),
        Err(err) => meta_err(err),
    }
}

async fn get_bucket(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path(bucket): Path<String>,
) -> Response {
    if let Err(msg) = validate_bucket_name(&bucket) {
        // Invalid names are unaddressable → 404 (no existence leak / traversal).
        let _ = msg;
        return (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "bucket not found".into(),
                code: Some("not_found"),
                object_count: None,
            }),
        )
            .into_response();
    }
    let Some(meta) = state.meta.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "metadata store unavailable".into(),
                code: Some("unavailable"),
                object_count: None,
            }),
        )
            .into_response();
    };
    match meta.get_bucket(&project.project_id, &bucket) {
        Ok(b) => (StatusCode::OK, Json(b)).into_response(),
        Err(err) => meta_err(err),
    }
}

async fn delete_bucket(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Path(bucket): Path<String>,
) -> Response {
    let rid = request_id(&headers);
    if validate_bucket_name(&bucket).is_err() {
        return (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "bucket not found".into(),
                code: Some("not_found"),
                object_count: None,
            }),
        )
            .into_response();
    }
    let Some(meta) = state.meta.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "metadata store unavailable".into(),
                code: Some("unavailable"),
                object_count: None,
            }),
        )
            .into_response();
    };
    match meta.delete_bucket(&project.project_id, &bucket) {
        Ok(()) => {
            info!(
                project_id = %project.project_id,
                bucket = %bucket,
                request_id = %rid,
                "bucket deleted"
            );
            StatusCode::NO_CONTENT.into_response()
        }
        Err(err) => meta_err(err),
    }
}
