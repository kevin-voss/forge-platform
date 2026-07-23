//! Cosine nearest-neighbor query REST handler (raw vector or text→Models embed).

use crate::api::collections::collection_err;
use crate::api::validate::validate_collection_name;
use crate::clients::ModelsClientError;
use crate::collections::QueryHit;
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
pub struct QueryRequest {
    #[serde(default)]
    pub vector: Option<Vec<f32>>,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub model: Option<String>,
    pub top_k: usize,
    #[serde(default)]
    pub filter: Option<serde_json::Value>,
}

#[derive(Debug, Serialize)]
pub struct QueryResponse {
    pub results: Vec<QueryHit>,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/collections/{name}/query", post(query))
}

fn request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-forge-request-id")
        .or_else(|| headers.get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string()
}

async fn query(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Path(name): Path<String>,
    Json(body): Json<QueryRequest>,
) -> Response {
    let rid = request_id(&headers);
    if validate_collection_name(&name).is_err() {
        return not_found("collection not found");
    }
    if body.top_k == 0 {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: "top_k must be >= 1".into(),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }
    if body.top_k > state.max_top_k {
        return (
            StatusCode::UNPROCESSABLE_ENTITY,
            Json(ErrorBody {
                error: format!("top_k {} exceeds cap {}", body.top_k, state.max_top_k),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }

    let has_vector = body.vector.as_ref().map(|v| !v.is_empty()).unwrap_or(false);
    let text = body
        .text
        .as_deref()
        .map(str::trim)
        .filter(|s| !s.is_empty());
    if has_vector == text.is_some() {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: "provide exactly one of vector or text".into(),
                code: Some("invalid"),
            }),
        )
            .into_response();
    }

    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };

    let query_vector: Vec<f32> = if let Some(t) = text {
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

        let started = std::time::Instant::now();
        let embedded = match models
            .embed(&model, &[t.to_string()], Some(project.project_id.as_str()))
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
            latency_ms,
            request_id = %rid,
            "embed-then-query"
        );

        embedded.embeddings.into_iter().next().unwrap_or_default()
    } else {
        body.vector.unwrap()
    };

    match collections.query(
        &project.project_id,
        &project.namespace,
        &name,
        &query_vector,
        body.top_k,
        body.filter.as_ref(),
    ) {
        Ok(outcome) => {
            state
                .metrics
                .memory_query_candidates
                .fetch_add(outcome.candidates_scanned as u64, Ordering::Relaxed);
            state
                .metrics
                .memory_query_latency_micros_total
                .fetch_add(outcome.latency.as_micros() as u64, Ordering::Relaxed);
            state
                .metrics
                .memory_query_count
                .fetch_add(1, Ordering::Relaxed);
            info!(
                project_id = %project.project_id,
                namespace = %project.namespace,
                collection = %name,
                top_k = body.top_k,
                results = outcome.results.len(),
                candidates = outcome.candidates_scanned,
                latency_ms = outcome.latency.as_secs_f64() * 1000.0,
                request_id = %rid,
                "query completed"
            );
            (
                StatusCode::OK,
                Json(QueryResponse {
                    results: outcome.results,
                }),
            )
                .into_response()
        }
        Err(crate::collections::CollectionError::NotFound) => not_found("collection not found"),
        Err(err) => collection_err(err),
    }
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
