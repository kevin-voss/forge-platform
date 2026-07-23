//! Record read/list/delete REST handlers (delete = tombstone; compaction on boot).

use crate::api::collections::collection_err;
use crate::api::validate::{validate_collection_name, validate_record_id};
use crate::collections::Record;
use crate::project::ProjectContext;
use crate::state::AppState;
use axum::extract::{Extension, Path, Query, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use tracing::info;

#[derive(Debug, Deserialize)]
pub struct ListRecordsQuery {
    #[serde(default)]
    pub offset: i64,
    pub limit: Option<i64>,
}

#[derive(Debug, Serialize)]
pub struct RecordListResponse {
    pub records: Vec<Record>,
    pub offset: i64,
    pub limit: i64,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/collections/{name}/records", get(list_records))
        .route(
            "/v1/collections/{name}/records/{id}",
            get(get_record).delete(delete_record),
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

async fn get_record(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path((name, id)): Path<(String, String)>,
) -> Response {
    if validate_collection_name(&name).is_err() || validate_record_id(&id).is_err() {
        return not_found("record not found");
    }
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    match collections.get_record(&project.project_id, &name, id.trim()) {
        Ok(rec) => (StatusCode::OK, Json(rec)).into_response(),
        Err(crate::collections::CollectionError::NotFound) => not_found("record not found"),
        Err(err) => collection_err(err),
    }
}

async fn list_records(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path(name): Path<String>,
    Query(query): Query<ListRecordsQuery>,
) -> Response {
    if validate_collection_name(&name).is_err() {
        return not_found("collection not found");
    }
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    let limit = query
        .limit
        .unwrap_or(state.list_page_size as i64)
        .clamp(1, state.list_page_size as i64);
    let offset = query.offset.max(0);
    match collections.list_records(&project.project_id, &name, offset, limit) {
        Ok(records) => (
            StatusCode::OK,
            Json(RecordListResponse {
                records,
                offset,
                limit,
            }),
        )
            .into_response(),
        Err(crate::collections::CollectionError::NotFound) => not_found("collection not found"),
        Err(err) => collection_err(err),
    }
}

async fn delete_record(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Path((name, id)): Path<(String, String)>,
) -> Response {
    let rid = request_id(&headers);
    if validate_collection_name(&name).is_err() || validate_record_id(&id).is_err() {
        return not_found("record not found");
    }
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    match collections.delete_record(&project.project_id, &name, id.trim()) {
        Ok(()) => {
            info!(
                project_id = %project.project_id,
                collection = %name,
                record_id = %id,
                request_id = %rid,
                "record deleted (tombstone)"
            );
            StatusCode::NO_CONTENT.into_response()
        }
        Err(crate::collections::CollectionError::NotFound) => not_found("record not found"),
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
