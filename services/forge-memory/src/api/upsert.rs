//! Batch upsert REST handler.

use crate::api::collections::collection_err;
use crate::api::validate::{validate_collection_name, validate_record_id};
use crate::project::ProjectContext;
use crate::state::AppState;
use axum::extract::{Extension, Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use std::sync::atomic::Ordering;
use tracing::info;

#[derive(Debug, Deserialize)]
pub struct UpsertRequest {
    pub records: Vec<UpsertRecord>,
}

#[derive(Debug, Deserialize)]
pub struct UpsertRecord {
    pub id: String,
    pub vector: Vec<f32>,
    #[serde(default = "default_metadata")]
    pub metadata: serde_json::Value,
    #[serde(default)]
    pub document_ref: Option<String>,
}

fn default_metadata() -> serde_json::Value {
    serde_json::json!({})
}

#[derive(Debug, Serialize)]
pub struct UpsertResponse {
    pub upserted: usize,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/collections/{name}/upsert", post(upsert))
}

fn request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-forge-request-id")
        .or_else(|| headers.get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string()
}

async fn upsert(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Path(name): Path<String>,
    Json(body): Json<UpsertRequest>,
) -> Response {
    let rid = request_id(&headers);
    if validate_collection_name(&name).is_err() {
        return not_found("collection not found");
    }
    if body.records.is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: "records must not be empty".into(),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }
    if body.records.len() > state.max_upsert_batch {
        return (
            StatusCode::UNPROCESSABLE_ENTITY,
            Json(ErrorBody {
                error: format!(
                    "upsert batch size {} exceeds cap {}",
                    body.records.len(),
                    state.max_upsert_batch
                ),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }
    for rec in &body.records {
        if let Err(msg) = validate_record_id(&rec.id) {
            return (
                StatusCode::BAD_REQUEST,
                Json(ErrorBody {
                    error: msg,
                    code: Some("invalid"),
                }),
            )
                .into_response();
        }
    }

    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };

    let batch: Vec<(String, Vec<f32>, serde_json::Value, Option<String>)> = body
        .records
        .into_iter()
        .map(|r| {
            (
                r.id.trim().to_string(),
                r.vector,
                r.metadata,
                r.document_ref,
            )
        })
        .collect();

    match collections.upsert_batch(&project.project_id, &name, &batch) {
        Ok(upserted) => {
            state
                .metrics
                .memory_upserts_total
                .fetch_add(upserted as u64, Ordering::Relaxed);
            state
                .metrics
                .memory_records_total
                .fetch_add(upserted as u64, Ordering::Relaxed);
            info!(
                project_id = %project.project_id,
                collection = %name,
                upserted,
                request_id = %rid,
                "upsert completed"
            );
            (StatusCode::OK, Json(UpsertResponse { upserted })).into_response()
        }
        Err(crate::collections::CollectionError::NotFound) => not_found("collection not found"),
        Err(err) => collection_err(err),
    }
}

fn not_found(msg: &str) -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("not_found"),
        }),
    )
        .into_response()
}

fn unavailable() -> Response {
    (
        StatusCode::SERVICE_UNAVAILABLE,
        Json(ErrorBody {
            error: "collection store unavailable".into(),
            code: Some("unavailable"),
        }),
    )
        .into_response()
}
