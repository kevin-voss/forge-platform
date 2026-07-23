//! Authorized audit query API.

use crate::audit::recorder::AuditRecorder;
use crate::auth::middleware::AuthPrincipal;
use crate::state::AppState;
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Extension, Json, Router};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tracing::error;

#[derive(Debug, Deserialize)]
pub struct AuditQuery {
    pub name: Option<String>,
    pub action: Option<String>,
    pub since: Option<String>,
    pub limit: Option<i64>,
}

/// Wire response — **no value field**.
#[derive(Debug, Serialize)]
pub struct AuditEventResponse {
    pub at: String,
    pub action: String,
    pub principal: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<i32>,
    pub result: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/projects/{project_id}/audit", get(list_audit_project))
        .route(
            "/v1/projects/{project_id}/envs/{environment}/audit",
            get(list_audit_env),
        )
}

fn bad_request(msg: impl Into<String>) -> axum::response::Response {
    (
        StatusCode::BAD_REQUEST,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("bad_request"),
        }),
    )
        .into_response()
}

fn parse_since(raw: Option<&str>) -> Result<Option<DateTime<Utc>>, &'static str> {
    match raw {
        None => Ok(None),
        Some(s) if s.trim().is_empty() => Ok(None),
        Some(s) => DateTime::parse_from_rfc3339(s.trim())
            .map(|dt| Some(dt.with_timezone(&Utc)))
            .map_err(|_| "since must be RFC3339"),
    }
}

async fn list_audit_project(
    State(state): State<AppState>,
    Path(project_id): Path<String>,
    Query(q): Query<AuditQuery>,
    principal: Option<Extension<AuthPrincipal>>,
) -> impl IntoResponse {
    let _ = principal;
    list_audit_inner(&state, &project_id, None, q).await
}

async fn list_audit_env(
    State(state): State<AppState>,
    Path((project_id, environment)): Path<(String, String)>,
    Query(q): Query<AuditQuery>,
    principal: Option<Extension<AuthPrincipal>>,
) -> impl IntoResponse {
    let _ = principal;
    if environment.trim().is_empty() {
        return bad_request("environment required");
    }
    list_audit_inner(&state, &project_id, Some(environment.as_str()), q).await
}

async fn list_audit_inner(
    state: &AppState,
    project_id: &str,
    environment: Option<&str>,
    q: AuditQuery,
) -> axum::response::Response {
    if project_id.trim().is_empty() {
        return bad_request("project_id required");
    }
    if !state.is_ready() {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
                code: Some("not_ready"),
            }),
        )
            .into_response();
    }
    let since = match parse_since(q.since.as_deref()) {
        Ok(v) => v,
        Err(msg) => return bad_request(msg),
    };
    let recorder = AuditRecorder::new(
        state.pool.clone(),
        state.audit_enabled,
        state.audit_strict,
        state.audit_metrics.clone(),
    );
    match recorder
        .query(
            project_id,
            environment,
            q.name.as_deref(),
            q.action.as_deref(),
            since,
            q.limit.unwrap_or(100),
        )
        .await
    {
        Ok(rows) => {
            let body: Vec<AuditEventResponse> = rows
                .into_iter()
                .map(|r| AuditEventResponse {
                    at: r.at.to_rfc3339(),
                    action: r.action,
                    principal: r.principal,
                    name: r.name,
                    version: r.version,
                    result: r.result,
                    source: r.source,
                    environment: r.environment,
                })
                .collect();
            // Contract: no value field on any item.
            debug_assert!(serde_json::to_value(&body)
                .ok()
                .and_then(|v| v.as_array().cloned())
                .map(|arr| arr.iter().all(|i| i.get("value").is_none()))
                .unwrap_or(true));
            (StatusCode::OK, Json(body)).into_response()
        }
        Err(err) => {
            error!(error = %err, "audit query failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                }),
            )
                .into_response()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn audit_response_schema_has_no_value_field() {
        let item = AuditEventResponse {
            at: "t".into(),
            action: "secret.access".into(),
            principal: "user:u1".into(),
            name: Some("DATABASE_PASSWORD".into()),
            version: Some(1),
            result: "ok".into(),
            source: Some("cli".into()),
            environment: Some("production".into()),
        };
        let v = serde_json::to_value(&item).unwrap();
        assert!(v.get("value").is_none());
        assert!(v.get("action").is_some());
        assert!(v.get("result").is_some());
    }
}
