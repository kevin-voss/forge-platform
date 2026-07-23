//! Cosine nearest-neighbor query REST handler.

use crate::api::collections::collection_err;
use crate::api::validate::validate_collection_name;
use crate::collections::QueryHit;
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
pub struct QueryRequest {
    pub vector: Vec<f32>,
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

    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };

    match collections.query(
        &project.project_id,
        &name,
        &body.vector,
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
