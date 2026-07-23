use crate::audit::hook::{principal_label, record, source_from_headers};
use crate::audit::AuditResult;
use crate::auth::middleware::AuthPrincipal;
use crate::config_store::store::ConfigStore;
use crate::state::AppState;
use axum::extract::{Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::IntoResponse;
use axum::routing::{get, put};
use axum::{Extension, Json, Router};
use serde::{Deserialize, Serialize};
use tracing::{error, info};

#[derive(Debug, Deserialize)]
pub struct SetConfigBody {
    pub value: String,
}

#[derive(Debug, Serialize)]
pub struct ConfigItemResponse {
    pub name: String,
    pub value: String,
    pub updated_at: String,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route(
            "/v1/projects/{project_id}/envs/{environment}/config",
            get(list_config),
        )
        .route(
            "/v1/projects/{project_id}/envs/{environment}/config/{name}",
            put(set_config).delete(delete_config),
        )
}

fn validate_scope(project_id: &str, environment: &str) -> Result<(), &'static str> {
    if project_id.trim().is_empty() {
        return Err("project_id required");
    }
    if environment.trim().is_empty() {
        return Err("environment required");
    }
    if project_id.contains('/') || environment.contains('/') {
        return Err("invalid scope");
    }
    Ok(())
}

/// Config names share secret naming: safe as env-var keys `[A-Za-z_][A-Za-z0-9_]*`.
fn validate_config_name(name: &str) -> Result<(), &'static str> {
    let mut chars = name.chars();
    let Some(first) = chars.next() else {
        return Err("config name required");
    };
    if !(first.is_ascii_alphabetic() || first == '_') {
        return Err("config name must start with A-Z, a-z, or _");
    }
    if !chars.all(|c| c.is_ascii_alphanumeric() || c == '_') {
        return Err("config name must be [A-Za-z_][A-Za-z0-9_]*");
    }
    if name.len() > 256 {
        return Err("config name too long");
    }
    Ok(())
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

fn not_ready() -> axum::response::Response {
    (
        StatusCode::SERVICE_UNAVAILABLE,
        Json(ErrorBody {
            error: "service not ready".into(),
            code: Some("not_ready"),
        }),
    )
        .into_response()
}

async fn set_config(
    State(state): State<AppState>,
    Path((project_id, environment, name)): Path<(String, String, String)>,
    headers: HeaderMap,
    principal: Option<Extension<AuthPrincipal>>,
    Json(body): Json<SetConfigBody>,
) -> impl IntoResponse {
    let principal_str = principal_label(principal.as_ref().map(|e| &e.0), &state.auth_mode);
    let source = source_from_headers(&headers);
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if let Err(msg) = validate_config_name(&name) {
        return bad_request(msg);
    }
    if body.value.len() > state.max_value_bytes {
        return (
            StatusCode::PAYLOAD_TOO_LARGE,
            Json(ErrorBody {
                error: format!(
                    "value exceeds FORGE_SECRETS_MAX_VALUE_BYTES ({})",
                    state.max_value_bytes
                ),
                code: Some("value_too_large"),
            }),
        )
            .into_response();
    }
    if !state.is_ready() {
        return not_ready();
    }
    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };
    let store = ConfigStore::new(pool.clone());
    match store
        .upsert(&project_id, &environment, &name, &body.value)
        .await
    {
        Ok(row) => {
            state
                .config_values_total
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            record(
                &state,
                &project_id,
                Some(&environment),
                "config.set",
                &principal_str,
                Some(&name),
                None,
                AuditResult::Ok,
                source.as_deref(),
            )
            .await;
            info!(
                project = %project_id,
                env = %environment,
                name = %name,
                "config value stored (plaintext; not a secret)"
            );
            (
                StatusCode::CREATED,
                Json(ConfigItemResponse {
                    name: row.name,
                    value: row.value,
                    updated_at: row.updated_at.to_rfc3339(),
                }),
            )
                .into_response()
        }
        Err(err) => {
            error!(error = %err, "upsert config failed");
            record(
                &state,
                &project_id,
                Some(&environment),
                "config.set",
                &principal_str,
                Some(&name),
                None,
                AuditResult::Error,
                source.as_deref(),
            )
            .await;
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

async fn list_config(
    State(state): State<AppState>,
    Path((project_id, environment)): Path<(String, String)>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if !state.is_ready() {
        return not_ready();
    }
    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };
    let store = ConfigStore::new(pool.clone());
    match store.list(&project_id, &environment).await {
        Ok(items) => {
            info!(
                project = %project_id,
                env = %environment,
                count = items.len(),
                "listed config values"
            );
            let body: Vec<ConfigItemResponse> = items
                .into_iter()
                .map(|i| ConfigItemResponse {
                    name: i.name,
                    value: i.value,
                    updated_at: i.updated_at.to_rfc3339(),
                })
                .collect();
            (StatusCode::OK, Json(body)).into_response()
        }
        Err(err) => {
            error!(error = %err, "list config failed");
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

async fn delete_config(
    State(state): State<AppState>,
    Path((project_id, environment, name)): Path<(String, String, String)>,
    headers: HeaderMap,
    principal: Option<Extension<AuthPrincipal>>,
) -> impl IntoResponse {
    let principal_str = principal_label(principal.as_ref().map(|e| &e.0), &state.auth_mode);
    let source = source_from_headers(&headers);
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if let Err(msg) = validate_config_name(&name) {
        return bad_request(msg);
    }
    if !state.is_ready() {
        return not_ready();
    }
    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };
    let store = ConfigStore::new(pool.clone());
    match store.delete(&project_id, &environment, &name).await {
        Ok(true) => {
            record(
                &state,
                &project_id,
                Some(&environment),
                "config.set",
                &principal_str,
                Some(&name),
                None,
                AuditResult::Ok,
                source.as_deref(),
            )
            .await;
            info!(
                project = %project_id,
                env = %environment,
                name = %name,
                "config value deleted"
            );
            StatusCode::NO_CONTENT.into_response()
        }
        Ok(false) => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "config not found".into(),
                code: Some("not_found"),
            }),
        )
            .into_response(),
        Err(err) => {
            error!(error = %err, "delete config failed");
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
    fn config_name_validation() {
        assert!(validate_config_name("FEATURE_X").is_ok());
        assert!(validate_config_name("_flag").is_ok());
        assert!(validate_config_name("").is_err());
        assert!(validate_config_name("1bad").is_err());
        assert!(validate_config_name("has-dash").is_err());
    }

    #[test]
    fn list_response_includes_value() {
        let item = ConfigItemResponse {
            name: "FEATURE_X".into(),
            value: "true".into(),
            updated_at: "t".into(),
        };
        let v = serde_json::to_value(&item).unwrap();
        assert_eq!(v.get("value").and_then(|x| x.as_str()), Some("true"));
    }
}
