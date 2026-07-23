use crate::audit::hook::{principal_label, record, source_from_headers};
use crate::audit::AuditResult;
use crate::auth::middleware::AuthPrincipal;
use crate::bindings::resolve::{resolve_for_service, ResolveError};
use crate::bindings::store::BindingStore;
use crate::state::{AppState, EnsureError};
use axum::extract::{Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::IntoResponse;
use axum::routing::{post, put};
use axum::{Extension, Json, Router};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use tracing::{error, info};

#[derive(Debug, Deserialize)]
pub struct PutBindingsBody {
    #[serde(default)]
    pub secrets: Vec<String>,
    #[serde(default)]
    pub config: Vec<String>,
}

#[derive(Debug, Serialize)]
pub struct BindingsResponse {
    pub secrets: Vec<String>,
    pub config: Vec<String>,
    pub updated_at: String,
}

#[derive(Debug, Serialize)]
pub struct ResolveResponse {
    pub env: BTreeMap<String, String>,
    pub version_fingerprint: String,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub missing: Option<Vec<MissingItem>>,
}

#[derive(Debug, Serialize)]
pub struct MissingItem {
    pub kind: &'static str,
    pub name: String,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route(
            "/v1/projects/{project_id}/envs/{environment}/services/{service}/bindings",
            put(put_bindings).get(get_bindings),
        )
        .route(
            "/v1/projects/{project_id}/envs/{environment}/services/{service}/resolve",
            post(resolve_service),
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

fn validate_service(service: &str) -> Result<(), &'static str> {
    if service.trim().is_empty() {
        return Err("service required");
    }
    if service.contains('/') {
        return Err("invalid service");
    }
    if service.len() > 256 {
        return Err("service name too long");
    }
    Ok(())
}

/// Env-var key names: `[A-Za-z_][A-Za-z0-9_]*`.
fn validate_env_name(name: &str, kind: &str) -> Result<(), String> {
    let mut chars = name.chars();
    let Some(first) = chars.next() else {
        return Err(format!("{kind} name required"));
    };
    if !(first.is_ascii_alphabetic() || first == '_') {
        return Err(format!("{kind} name must start with A-Z, a-z, or _"));
    }
    if !chars.all(|c| c.is_ascii_alphanumeric() || c == '_') {
        return Err(format!("{kind} name must be [A-Za-z_][A-Za-z0-9_]*"));
    }
    if name.len() > 256 {
        return Err(format!("{kind} name too long"));
    }
    Ok(())
}

fn bad_request(msg: impl Into<String>) -> axum::response::Response {
    (
        StatusCode::BAD_REQUEST,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("bad_request"),
            missing: None,
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
            missing: None,
        }),
    )
        .into_response()
}

async fn put_bindings(
    State(state): State<AppState>,
    Path((project_id, environment, service)): Path<(String, String, String)>,
    Json(body): Json<PutBindingsBody>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if let Err(msg) = validate_service(&service) {
        return bad_request(msg);
    }
    for name in &body.secrets {
        if let Err(msg) = validate_env_name(name, "secret") {
            return bad_request(msg);
        }
    }
    for name in &body.config {
        if let Err(msg) = validate_env_name(name, "config") {
            return bad_request(msg);
        }
    }
    if !state.is_ready() {
        return not_ready();
    }
    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };
    let store = BindingStore::new(pool.clone());
    match store
        .upsert(
            &project_id,
            &environment,
            &service,
            &body.secrets,
            &body.config,
        )
        .await
    {
        Ok(row) => {
            info!(
                project = %project_id,
                env = %environment,
                service = %service,
                secrets = ?row.secret_names,
                config = ?row.config_names,
                "service bindings upserted"
            );
            (
                StatusCode::OK,
                Json(BindingsResponse {
                    secrets: row.secret_names,
                    config: row.config_names,
                    updated_at: row.updated_at.to_rfc3339(),
                }),
            )
                .into_response()
        }
        Err(err) => {
            error!(error = %err, "upsert bindings failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                    missing: None,
                }),
            )
                .into_response()
        }
    }
}

async fn get_bindings(
    State(state): State<AppState>,
    Path((project_id, environment, service)): Path<(String, String, String)>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if let Err(msg) = validate_service(&service) {
        return bad_request(msg);
    }
    if !state.is_ready() {
        return not_ready();
    }
    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };
    let store = BindingStore::new(pool.clone());
    match store.get(&project_id, &environment, &service).await {
        Ok(Some(row)) => (
            StatusCode::OK,
            Json(BindingsResponse {
                secrets: row.secret_names,
                config: row.config_names,
                updated_at: row.updated_at.to_rfc3339(),
            }),
        )
            .into_response(),
        Ok(None) => (
            StatusCode::OK,
            Json(BindingsResponse {
                secrets: vec![],
                config: vec![],
                updated_at: chrono::Utc::now().to_rfc3339(),
            }),
        )
            .into_response(),
        Err(err) => {
            error!(error = %err, "get bindings failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                    missing: None,
                }),
            )
                .into_response()
        }
    }
}

async fn resolve_service(
    State(state): State<AppState>,
    Path((project_id, environment, service)): Path<(String, String, String)>,
    headers: HeaderMap,
    principal: Option<Extension<AuthPrincipal>>,
) -> impl IntoResponse {
    let principal_str = principal_label(principal.as_ref().map(|e| &e.0), &state.auth_mode);
    let source = source_from_headers(&headers);
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    if let Err(msg) = validate_service(&service) {
        return bad_request(msg);
    }
    if !state.is_ready() {
        return not_ready();
    }

    let (data_key, _) = match state.unwrap_project_data_key(&project_id).await {
        Ok(v) => v,
        Err(EnsureError::NotReady) => return not_ready(),
        Err(_) => {
            error!(project = %project_id, "data key unavailable for resolve");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_error"),
                    missing: None,
                }),
            )
                .into_response();
        }
    };

    let Some(pool) = state.pool.as_ref() else {
        return not_ready();
    };

    match resolve_for_service(
        pool,
        state.aead_alg,
        &data_key,
        &project_id,
        &environment,
        &service,
    )
    .await
    {
        Ok(bundle) => {
            // Register resolved secret values for log masking (in-memory only).
            for (key, value) in &bundle.env {
                if bundle.keys.iter().any(|k| k == key) {
                    // Only mask bound secret names, not config (config is non-secret).
                    // Heuristic: register all resolved env values that look like secrets
                    // by matching secret_names from keys that were decrypted — register all
                    // values from the bundle that came from secrets via key list intersection
                    // with known secret bindings. Safer: register every value; config may be
                    // short flags like "true" (len < 3 skipped by register).
                    state.known_secrets.register(value);
                }
            }
            record(
                &state,
                &project_id,
                Some(&environment),
                "resolve",
                &principal_str,
                Some(&service),
                None,
                AuditResult::Ok,
                source.as_deref(),
            )
            .await;
            state
                .secret_resolves_total
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            // Intentionally do not log env values.
            info!(
                project = %project_id,
                env = %environment,
                service = %service,
                keys = ?bundle.keys,
                version_fingerprint = %bundle.version_fingerprint,
                "secrets.resolve completed"
            );
            (
                StatusCode::OK,
                Json(ResolveResponse {
                    env: bundle.env,
                    version_fingerprint: bundle.version_fingerprint,
                }),
            )
                .into_response()
        }
        Err(ResolveError::Missing(missing)) => {
            let names: Vec<String> = missing.iter().map(|m| m.name.clone()).collect();
            info!(
                project = %project_id,
                env = %environment,
                service = %service,
                missing = ?names,
                "resolve missing bound secrets/config"
            );
            (
                StatusCode::UNPROCESSABLE_ENTITY,
                Json(ErrorBody {
                    error: format!("missing bound names: {}", names.join(", ")),
                    code: Some("missing_bindings"),
                    missing: Some(
                        missing
                            .into_iter()
                            .map(|m| MissingItem {
                                kind: m.kind,
                                name: m.name,
                            })
                            .collect(),
                    ),
                }),
            )
                .into_response()
        }
        Err(ResolveError::Storage(err)) => {
            error!(error = %err, "resolve storage failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                    missing: None,
                }),
            )
                .into_response()
        }
        Err(ResolveError::Crypto(err)) => {
            error!(error = %err, "resolve crypto failed");
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_error"),
                    missing: None,
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
    fn resolve_response_serializes_env_and_fingerprint() {
        let mut env = BTreeMap::new();
        env.insert("DATABASE_PASSWORD".into(), "pw1".into());
        env.insert("FEATURE_X".into(), "true".into());
        let body = ResolveResponse {
            env,
            version_fingerprint: "abc".into(),
        };
        let v = serde_json::to_value(&body).unwrap();
        assert!(v.get("env").is_some());
        assert_eq!(
            v.get("version_fingerprint").and_then(|x| x.as_str()),
            Some("abc")
        );
    }

    #[test]
    fn env_name_validation() {
        assert!(validate_env_name("DATABASE_PASSWORD", "secret").is_ok());
        assert!(validate_env_name("FEATURE_X", "config").is_ok());
        assert!(validate_env_name("bad-name", "secret").is_err());
    }
}
