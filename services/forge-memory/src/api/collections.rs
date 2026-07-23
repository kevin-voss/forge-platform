//! Collection lifecycle REST handlers.

use crate::api::validate::validate_collection_name;
use crate::collections::CollectionError;
use crate::meta::Collection;
use crate::scope::{normalize_namespace, ProjectContext};
use crate::state::AppState;
use axum::extract::{Extension, Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use std::sync::atomic::Ordering;
use tracing::info;

#[derive(Debug, Deserialize)]
pub struct CreateCollectionRequest {
    pub name: String,
    pub dim: i64,
    #[serde(default = "default_distance")]
    pub distance: String,
    /// Optional sub-namespace; falls back to request scope (`?namespace=`).
    #[serde(default)]
    pub namespace: Option<String>,
}

fn default_distance() -> String {
    "cosine".into()
}

#[derive(Debug, Serialize)]
pub struct CollectionListResponse {
    pub collections: Vec<Collection>,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route(
            "/v1/collections",
            post(create_collection).get(list_collections),
        )
        .route(
            "/v1/collections/{name}",
            get(get_collection).delete(delete_collection),
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

pub(crate) fn collection_err(err: CollectionError) -> Response {
    match err {
        CollectionError::NotFound => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "collection not found".into(),
                code: Some("not_found"),
            }),
        )
            .into_response(),
        CollectionError::Conflict(msg) => (
            StatusCode::CONFLICT,
            Json(ErrorBody {
                error: msg,
                code: Some("conflict"),
            }),
        )
            .into_response(),
        CollectionError::Invalid(msg) => (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: msg,
                code: Some("invalid"),
            }),
        )
            .into_response(),
        CollectionError::DimensionMismatch { expected, got } => (
            StatusCode::UNPROCESSABLE_ENTITY,
            Json(ErrorBody {
                error: format!("vector dimension mismatch: expected {expected}, got {got}"),
                code: Some("dimension_mismatch"),
            }),
        )
            .into_response(),
        CollectionError::Corrupt(msg) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(ErrorBody {
                error: msg,
                code: Some("corrupt"),
            }),
        )
            .into_response(),
        CollectionError::Internal(msg) => {
            tracing::warn!(error = %msg, "collection store error");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "internal error".into(),
                    code: Some("internal"),
                }),
            )
                .into_response()
        }
    }
}

async fn create_collection(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Json(body): Json<CreateCollectionRequest>,
) -> Response {
    let rid = request_id(&headers);
    if let Err(msg) = validate_collection_name(&body.name) {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorBody {
                error: msg,
                code: Some("invalid_name"),
            }),
        )
            .into_response();
    }
    let namespace = if let Some(raw) = body.namespace.as_deref() {
        match normalize_namespace(raw) {
            Ok(ns) => ns,
            Err(msg) => {
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
    } else {
        project.namespace.clone()
    };
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    match collections.create_collection(
        &project.project_id,
        &namespace,
        body.name.trim(),
        body.dim,
        body.distance.trim(),
    ) {
        Ok(collection) => {
            state
                .metrics
                .memory_collections_total
                .fetch_add(1, Ordering::Relaxed);
            info!(
                project_id = %project.project_id,
                namespace = %collection.namespace,
                collection = %collection.name,
                dim = collection.dim,
                request_id = %rid,
                "collection created"
            );
            (StatusCode::CREATED, Json(collection)).into_response()
        }
        Err(err) => collection_err(err),
    }
}

async fn list_collections(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
) -> Response {
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    // When caller supplied `?namespace=`, middleware sets it; empty means list all namespaces.
    // Spec: optional namespace filter — empty default lists the whole project.
    let filter = if project.namespace.is_empty() {
        None
    } else {
        Some(project.namespace.as_str())
    };
    match collections.list_collections(&project.project_id, filter) {
        Ok(list) => (
            StatusCode::OK,
            Json(CollectionListResponse { collections: list }),
        )
            .into_response(),
        Err(err) => collection_err(err),
    }
}

async fn get_collection(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path(name): Path<String>,
) -> Response {
    if validate_collection_name(&name).is_err() {
        return (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "collection not found".into(),
                code: Some("not_found"),
            }),
        )
            .into_response();
    }
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    match collections.get_collection(&project.project_id, &project.namespace, &name) {
        Ok(c) => (StatusCode::OK, Json(c)).into_response(),
        Err(err) => collection_err(err),
    }
}

async fn delete_collection(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    headers: HeaderMap,
    Path(name): Path<String>,
) -> Response {
    let rid = request_id(&headers);
    if validate_collection_name(&name).is_err() {
        return (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "collection not found".into(),
                code: Some("not_found"),
            }),
        )
            .into_response();
    }
    let Ok(collections) = state.ensure_collections() else {
        return unavailable();
    };
    match collections.delete_collection(&project.project_id, &project.namespace, &name) {
        Ok(()) => {
            state
                .metrics
                .memory_collections_total
                .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |n| {
                    Some(n.saturating_sub(1))
                })
                .ok();
            info!(
                project_id = %project.project_id,
                namespace = %project.namespace,
                collection = %name,
                request_id = %rid,
                "collection deleted"
            );
            StatusCode::NO_CONTENT.into_response()
        }
        Err(err) => collection_err(err),
    }
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
