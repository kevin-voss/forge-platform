//! Batch upsert REST handler (raw vectors or text→Models embed).

use crate::api::collections::collection_err;
use crate::api::validate::{validate_collection_name, validate_record_id};
use crate::clients::ModelsClientError;
use crate::scope::ProjectContext;
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
    #[serde(default)]
    pub records: Option<Vec<UpsertRecord>>,
    #[serde(default)]
    pub items: Option<Vec<UpsertTextItem>>,
    #[serde(default)]
    pub model: Option<String>,
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

#[derive(Debug, Deserialize)]
pub struct UpsertTextItem {
    pub id: String,
    pub text: String,
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

    let has_records = body
        .records
        .as_ref()
        .map(|r| !r.is_empty())
        .unwrap_or(false);
    let has_items = body.items.as_ref().map(|r| !r.is_empty()).unwrap_or(false);
    if has_records == has_items {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: "provide exactly one of non-empty records (vectors) or items (text)".into(),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }

    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };

    let batch: Vec<(String, Vec<f32>, serde_json::Value, Option<String>)> = if has_records {
        let records = body.records.unwrap();
        if records.len() > state.max_upsert_batch {
            return batch_too_large(records.len(), state.max_upsert_batch);
        }
        for rec in &records {
            if let Err(msg) = validate_record_id(&rec.id) {
                return bad_request(msg);
            }
        }
        records
            .into_iter()
            .map(|r| {
                (
                    r.id.trim().to_string(),
                    r.vector,
                    r.metadata,
                    r.document_ref,
                )
            })
            .collect()
    } else {
        let items = body.items.unwrap();
        if items.len() > state.max_upsert_batch {
            return batch_too_large(items.len(), state.max_upsert_batch);
        }
        for item in &items {
            if let Err(msg) = validate_record_id(&item.id) {
                return bad_request(msg);
            }
            if item.text.trim().is_empty() {
                return bad_request("item text must not be empty".into());
            }
        }

        let model = body
            .model
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .unwrap_or(state.default_embed_model.as_str())
            .to_string();

        let Some(models) = state.models.as_ref() else {
            return embedding_unavailable("embedding backend unavailable: not configured");
        };

        let collection =
            match collections.get_collection(&project.project_id, &project.namespace, &name) {
                Ok(c) => c,
                Err(crate::collections::CollectionError::NotFound) => {
                    return not_found("collection not found");
                }
                Err(err) => return collection_err(err),
            };

        let texts: Vec<String> = items.iter().map(|i| i.text.clone()).collect();
        let started = std::time::Instant::now();
        let embedded = match models
            .embed(&model, &texts, Some(project.project_id.as_str()))
            .await
        {
            Ok(r) => r,
            Err(ModelsClientError::Unavailable(msg)) => return embedding_unavailable(&msg),
            Err(ModelsClientError::BadResponse(msg)) => {
                return (
                    StatusCode::BAD_GATEWAY,
                    Json(ErrorBody {
                        error: msg,
                        code: Some("embedding_backend_unavailable"),
                    }),
                )
                    .into_response();
            }
        };
        state
            .metrics
            .memory_embed_calls_total
            .fetch_add(1, Ordering::Relaxed);

        let expected_dim = collection.dim as usize;
        if embedded.dim != expected_dim {
            return (
                StatusCode::UNPROCESSABLE_ENTITY,
                Json(ErrorBody {
                    error: format!(
                        "vector dimension mismatch: expected {expected_dim}, got {}",
                        embedded.dim
                    ),
                    code: Some("dimension_mismatch"),
                }),
            )
                .into_response();
        }

        let latency_ms = started.elapsed().as_secs_f64() * 1000.0;
        info!(
            project_id = %project.project_id,
            namespace = %project.namespace,
            collection = %name,
            model = %embedded.model,
            dim = embedded.dim,
            batch = items.len(),
            latency_ms,
            request_id = %rid,
            "embed-then-upsert"
        );

        items
            .into_iter()
            .zip(embedded.embeddings.into_iter())
            .map(|(item, vector)| {
                (
                    item.id.trim().to_string(),
                    vector,
                    item.metadata,
                    item.document_ref,
                )
            })
            .collect()
    };

    match collections.upsert_batch(&project.project_id, &project.namespace, &name, &batch) {
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
                namespace = %project.namespace,
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

fn bad_request(msg: String) -> Response {
    (
        StatusCode::BAD_REQUEST,
        Json(ErrorBody {
            error: msg,
            code: Some("invalid"),
        }),
    )
        .into_response()
}

fn batch_too_large(got: usize, cap: usize) -> Response {
    (
        StatusCode::UNPROCESSABLE_ENTITY,
        Json(ErrorBody {
            error: format!("upsert batch size {got} exceeds cap {cap}"),
            code: Some("invalid"),
        }),
    )
        .into_response()
}

fn embedding_unavailable(msg: &str) -> Response {
    (
        StatusCode::SERVICE_UNAVAILABLE,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("embedding_backend_unavailable"),
        }),
    )
        .into_response()
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
