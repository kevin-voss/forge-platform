//! `POST .../objects/{key}/sign` — issue HMAC signed access tokens (13.05).

use crate::api::validate::{validate_bucket_name, validate_object_key};
use crate::meta::MetaError;
use crate::project::ProjectContext;
use crate::signing::{issue_token, SignError};
use crate::state::AppState;
use axum::extract::{Extension, Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::Json;
use chrono::{TimeZone, Utc};
use serde::{Deserialize, Serialize};
use std::sync::atomic::Ordering;
use tracing::info;

#[derive(Debug, Deserialize)]
pub struct SignRequest {
    pub method: String,
    pub ttl_seconds: u64,
}

#[derive(Debug, Serialize)]
pub struct SignResponse {
    pub token: String,
    pub url: String,
    pub expires_at: String,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

fn err_json(status: StatusCode, error: impl Into<String>, code: &'static str) -> Response {
    (
        status,
        Json(ErrorBody {
            error: error.into(),
            code: Some(code),
        }),
    )
        .into_response()
}

fn request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-forge-request-id")
        .or_else(|| headers.get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string()
}

/// Strip a trailing `/sign` segment from a catch-all object key path.
pub fn strip_sign_suffix(raw_key: &str) -> Option<&str> {
    let key = raw_key.trim_start_matches('/');
    if key == "sign" {
        return None;
    }
    key.strip_suffix("/sign").filter(|k| !k.is_empty())
}

fn build_signed_url(bucket: &str, key: &str, token: &str) -> String {
    // Path-absolute URL; object keys may contain `/` (already path segments).
    format!("/v1/buckets/{bucket}/objects/{key}?token={token}")
}

/// Handle `POST /v1/buckets/{bucket}/objects/{*key}` when `key` ends with `/sign`.
pub async fn post_sign(
    State(state): State<AppState>,
    Extension(project): Extension<ProjectContext>,
    Path((bucket, raw_key)): Path<(String, String)>,
    headers: HeaderMap,
    Json(body): Json<SignRequest>,
) -> Response {
    let rid = request_id(&headers);

    let Some(signing) = state.signing.as_ref() else {
        return err_json(
            StatusCode::SERVICE_UNAVAILABLE,
            "signing key not configured",
            "signing_unavailable",
        );
    };

    let Some(key) = strip_sign_suffix(&raw_key) else {
        return err_json(
            StatusCode::METHOD_NOT_ALLOWED,
            "POST is only supported for .../objects/{key}/sign",
            "method_not_allowed",
        );
    };

    if validate_bucket_name(&bucket).is_err() {
        return err_json(StatusCode::NOT_FOUND, "bucket not found", "not_found");
    }
    if validate_object_key(key).is_err() {
        return err_json(
            StatusCode::BAD_REQUEST,
            "invalid object key",
            "invalid_key",
        );
    }

    let Some(meta) = state.meta.as_ref() else {
        return err_json(
            StatusCode::SERVICE_UNAVAILABLE,
            "metadata store unavailable",
            "unavailable",
        );
    };

    match meta.get_bucket(&project.project_id, &bucket) {
        Ok(_) => {}
        Err(MetaError::NotFound) => {
            return err_json(StatusCode::NOT_FOUND, "bucket not found", "not_found");
        }
        Err(MetaError::Internal(msg)) => {
            tracing::warn!(error = %msg, "metadata store error");
            return err_json(
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal error",
                "internal",
            );
        }
        Err(err) => {
            return err_json(StatusCode::BAD_REQUEST, err.to_string(), "invalid");
        }
    }

    // Object need not exist yet (upload tokens); existence is not required to sign.
    let now = (state.clock)();
    let (token, claims) = match issue_token(
        signing,
        &body.method,
        &project.project_id,
        &bucket,
        key,
        body.ttl_seconds,
        now,
    ) {
        Ok(v) => v,
        Err(SignError::TtlTooLarge { max }) => {
            return err_json(
                StatusCode::BAD_REQUEST,
                format!("ttl_seconds exceeds maximum of {max}"),
                "ttl_too_large",
            );
        }
        Err(SignError::InvalidMethod) => {
            return err_json(
                StatusCode::BAD_REQUEST,
                "method must be GET or PUT",
                "invalid_method",
            );
        }
        Err(SignError::InvalidTtl) => {
            return err_json(
                StatusCode::BAD_REQUEST,
                "ttl_seconds must be a positive integer",
                "invalid_ttl",
            );
        }
    };

    state
        .metrics
        .storage_tokens_issued_total
        .fetch_add(1, Ordering::Relaxed);

    let expires_at = Utc
        .timestamp_opt(claims.exp, 0)
        .single()
        .unwrap_or_else(Utc::now)
        .to_rfc3339();

    info!(
        project_id = %project.project_id,
        bucket = %bucket,
        key = %key,
        method = %claims.method,
        exp = claims.exp,
        request_id = %rid,
        "signed access token issued"
    );

    let url = build_signed_url(&bucket, key, &token);
    (
        StatusCode::OK,
        Json(SignResponse {
            token,
            url,
            expires_at,
        }),
    )
        .into_response()
}
