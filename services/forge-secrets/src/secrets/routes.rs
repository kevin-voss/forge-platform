use crate::secrets::cipher::{decrypt, encrypt};
use crate::secrets::store::{NewSecretVersion, SecretStore};
use crate::state::{AppState, EnsureError};
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{get, put};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use tracing::{error, info};

#[derive(Debug, Deserialize)]
pub struct SetSecretBody {
    pub value: String,
}

#[derive(Debug, Serialize)]
pub struct SetSecretResponse {
    pub name: String,
    pub version: i32,
}

#[derive(Debug, Serialize)]
pub struct SecretListItemResponse {
    pub name: String,
    pub version: i32,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Serialize)]
pub struct SecretVersionResponse {
    pub version: i32,
    pub created_at: String,
}

#[derive(Debug, Serialize)]
pub struct SecretMetadataResponse {
    pub name: String,
    pub current_version: i32,
    pub versions: Vec<SecretVersionResponse>,
}

#[derive(Debug, Deserialize)]
pub struct AccessBody {
    #[serde(default)]
    pub version: Option<i32>,
}

#[derive(Debug, Deserialize)]
pub struct AccessQuery {
    pub version: Option<i32>,
}

#[derive(Debug, Serialize)]
pub struct AccessResponse {
    pub name: String,
    pub version: i32,
    pub value: String,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<&'static str>,
}

pub fn router() -> Router<AppState> {
    // Axum/matchit forbids `{name}:access` (one param per segment). External contract
    // remains `.../secrets/{name}:access`; we capture the whole segment and strip the suffix.
    Router::new()
        .route(
            "/v1/projects/{project_id}/envs/{environment}/secrets",
            get(list_secrets),
        )
        .route(
            "/v1/projects/{project_id}/envs/{environment}/secrets/{raw}",
            put(set_secret).get(get_secret_metadata).post(access_secret),
        )
}

fn validate_scope(project_id: &str, environment: &str) -> Result<(), &'static str> {
    if project_id.trim().is_empty() {
        return Err("project_id required");
    }
    if environment.trim().is_empty() {
        return Err("environment required");
    }
    // Basic path-scoped guard (full Identity enforcement in 10.03).
    if project_id.contains('/') || environment.contains('/') {
        return Err("invalid scope");
    }
    Ok(())
}

/// Secret names must be safe as env-var keys: `[A-Za-z_][A-Za-z0-9_]*`.
fn validate_secret_name(name: &str) -> Result<(), &'static str> {
    let mut chars = name.chars();
    let Some(first) = chars.next() else {
        return Err("secret name required");
    };
    if !(first.is_ascii_alphabetic() || first == '_') {
        return Err("secret name must start with A-Z, a-z, or _");
    }
    if !chars.all(|c| c.is_ascii_alphanumeric() || c == '_') {
        return Err("secret name must be [A-Za-z_][A-Za-z0-9_]*");
    }
    if name.len() > 256 {
        return Err("secret name too long");
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

async fn set_secret(
    State(state): State<AppState>,
    Path((project_id, environment, raw)): Path<(String, String, String)>,
    Json(body): Json<SetSecretBody>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    let name = raw;
    if let Err(msg) = validate_secret_name(&name) {
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

    let (data_key, data_key_version) = match state.unwrap_project_data_key(&project_id).await {
        Ok(v) => v,
        Err(EnsureError::NotReady) => {
            return (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(ErrorBody {
                    error: "service not ready".into(),
                    code: Some("not_ready"),
                }),
            )
                .into_response();
        }
        Err(EnsureError::Storage(err)) | Err(EnsureError::Crypto(err)) => {
            error!(project = %project_id, error = %err, "data key unwrap failed");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_error"),
                }),
            )
                .into_response();
        }
        Err(EnsureError::NotFound) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "data key missing after ensure".into(),
                    code: Some("crypto_error"),
                }),
            )
                .into_response();
        }
    };

    let encrypted = match encrypt(state.aead_alg, &data_key, body.value.as_bytes()) {
        Ok(v) => v,
        Err(err) => {
            error!(project = %project_id, env = %environment, name = %name, error = %err, "encrypt failed");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_error"),
                }),
            )
                .into_response();
        }
    };
    let Some(pool) = state.pool.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
                code: Some("not_ready"),
            }),
        )
            .into_response();
    };
    let store = SecretStore::new(pool.clone());
    let version = match store.next_version(&project_id, &environment, &name).await {
        Ok(v) => v,
        Err(err) => {
            error!(error = %err, "next_version failed");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                }),
            )
                .into_response();
        }
    };

    match store
        .insert_version(&NewSecretVersion {
            project_id: &project_id,
            environment: &environment,
            name: &name,
            version,
            ciphertext: &encrypted.ciphertext,
            nonce: &encrypted.nonce,
            data_key_version,
        })
        .await
    {
        Ok(row) => {
            state
                .secrets_total
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            info!(
                project = %project_id,
                env = %environment,
                name = %name,
                version = row.version,
                data_key_version = data_key_version,
                "secret version stored"
            );
            (
                StatusCode::CREATED,
                Json(SetSecretResponse {
                    name: row.name,
                    version: row.version,
                }),
            )
                .into_response()
        }
        Err(err) => {
            error!(error = %err, "insert secret failed");
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

async fn list_secrets(
    State(state): State<AppState>,
    Path((project_id, environment)): Path<(String, String)>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
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
    let Some(pool) = state.pool.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
                code: Some("not_ready"),
            }),
        )
            .into_response();
    };
    let store = SecretStore::new(pool.clone());
    match store.list_metadata(&project_id, &environment).await {
        Ok(items) => {
            info!(
                project = %project_id,
                env = %environment,
                count = items.len(),
                "listed secret metadata"
            );
            let body: Vec<SecretListItemResponse> = items
                .into_iter()
                .map(|i| SecretListItemResponse {
                    name: i.name,
                    version: i.version,
                    created_at: i.created_at.to_rfc3339(),
                    updated_at: i.updated_at.to_rfc3339(),
                })
                .collect();
            (StatusCode::OK, Json(body)).into_response()
        }
        Err(err) => {
            error!(error = %err, "list secrets failed");
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

async fn get_secret_metadata(
    State(state): State<AppState>,
    Path((project_id, environment, raw)): Path<(String, String, String)>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    let name = raw;
    if let Err(msg) = validate_secret_name(&name) {
        return bad_request(msg);
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
    let Some(pool) = state.pool.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
                code: Some("not_ready"),
            }),
        )
            .into_response();
    };
    let store = SecretStore::new(pool.clone());
    match store
        .version_history(&project_id, &environment, &name)
        .await
    {
        Ok(history) if history.is_empty() => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "secret not found".into(),
                code: Some("not_found"),
            }),
        )
            .into_response(),
        Ok(history) => {
            let current_version = history.last().map(|v| v.version).unwrap_or(0);
            info!(
                project = %project_id,
                env = %environment,
                name = %name,
                current_version,
                versions = history.len(),
                "secret metadata"
            );
            (
                StatusCode::OK,
                Json(SecretMetadataResponse {
                    name,
                    current_version,
                    versions: history
                        .into_iter()
                        .map(|v| SecretVersionResponse {
                            version: v.version,
                            created_at: v.created_at.to_rfc3339(),
                        })
                        .collect(),
                }),
            )
                .into_response()
        }
        Err(err) => {
            error!(error = %err, "get secret metadata failed");
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

async fn access_secret(
    State(state): State<AppState>,
    Path((project_id, environment, raw)): Path<(String, String, String)>,
    Query(query): Query<AccessQuery>,
    body: Option<Json<AccessBody>>,
) -> impl IntoResponse {
    if let Err(msg) = validate_scope(&project_id, &environment) {
        return bad_request(msg);
    }
    let Some(name) = raw.strip_suffix(":access").map(str::to_string) else {
        return (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: "use POST .../secrets/{name}:access to reveal".into(),
                code: Some("not_found"),
            }),
        )
            .into_response();
    };
    if let Err(msg) = validate_secret_name(&name) {
        return bad_request(msg);
    }

    // Basic authorized-path guard: service must be ready. Full Identity authz in 10.03.
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

    let requested_version = body.and_then(|Json(b)| b.version).or(query.version);

    let (data_key, _) = match state.unwrap_project_data_key(&project_id).await {
        Ok(v) => v,
        Err(EnsureError::NotReady) => {
            return (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(ErrorBody {
                    error: "service not ready".into(),
                    code: Some("not_ready"),
                }),
            )
                .into_response();
        }
        Err(EnsureError::NotFound) | Err(EnsureError::Storage(_)) | Err(EnsureError::Crypto(_)) => {
            error!(project = %project_id, "data key unavailable for access");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_error"),
                }),
            )
                .into_response();
        }
    };

    let Some(pool) = state.pool.as_ref() else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "service not ready".into(),
                code: Some("not_ready"),
            }),
        )
            .into_response();
    };
    let store = SecretStore::new(pool.clone());
    let row = match store
        .fetch_for_decrypt(&project_id, &environment, &name, requested_version)
        .await
    {
        Ok(None) => {
            return (
                StatusCode::NOT_FOUND,
                Json(ErrorBody {
                    error: "secret not found".into(),
                    code: Some("not_found"),
                }),
            )
                .into_response();
        }
        Ok(Some(row)) => row,
        Err(err) => {
            error!(error = %err, "fetch secret for access failed");
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "storage error".into(),
                    code: Some("storage_error"),
                }),
            )
                .into_response();
        }
    };

    let plaintext = match decrypt(state.aead_alg, &data_key, &row.ciphertext, &row.nonce) {
        Ok(bytes) => match String::from_utf8(bytes) {
            Ok(s) => s,
            Err(_) => {
                error!(
                    project = %project_id,
                    env = %environment,
                    name = %name,
                    version = row.version,
                    "decrypt produced non-utf8 (refusing to return garbage)"
                );
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    Json(ErrorBody {
                        error: "crypto error".into(),
                        code: Some("crypto_decrypt_failed"),
                    }),
                )
                    .into_response();
            }
        },
        Err(err) => {
            error!(
                project = %project_id,
                env = %environment,
                name = %name,
                version = row.version,
                error = %err,
                "decrypt failed"
            );
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(ErrorBody {
                    error: "crypto error".into(),
                    code: Some("crypto_decrypt_failed"),
                }),
            )
                .into_response();
        }
    };

    // Audit hook stub (full audit trail in 10.06) — never log the value.
    audit_access_stub(&project_id, &environment, &name, row.version);
    state
        .secret_access_total
        .fetch_add(1, std::sync::atomic::Ordering::Relaxed);

    info!(
        project = %project_id,
        env = %environment,
        name = %name,
        version = row.version,
        "secret accessed"
    );

    (
        StatusCode::OK,
        Json(AccessResponse {
            name: row.name,
            version: row.version,
            value: plaintext,
        }),
    )
        .into_response()
}

/// Placeholder access-audit hook; real persistence lands in 10.06.
fn audit_access_stub(project_id: &str, environment: &str, name: &str, version: i32) {
    info!(
        audit = true,
        action = "secret.access",
        project = %project_id,
        env = %environment,
        name = %name,
        version,
        "audit stub: secret access"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn secret_name_validation() {
        assert!(validate_secret_name("DATABASE_PASSWORD").is_ok());
        assert!(validate_secret_name("_private").is_ok());
        assert!(validate_secret_name("").is_err());
        assert!(validate_secret_name("1bad").is_err());
        assert!(validate_secret_name("has-dash").is_err());
        assert!(validate_secret_name("has.value").is_err());
    }

    #[test]
    fn list_response_schema_has_no_value_field() {
        let item = SecretListItemResponse {
            name: "X".into(),
            version: 1,
            created_at: "t".into(),
            updated_at: "t".into(),
        };
        let v = serde_json::to_value(&item).unwrap();
        assert!(v.get("value").is_none());
        assert!(v.get("name").is_some());
        assert!(v.get("version").is_some());
    }

    #[test]
    fn metadata_response_schema_has_no_value_field() {
        let meta = SecretMetadataResponse {
            name: "X".into(),
            current_version: 2,
            versions: vec![SecretVersionResponse {
                version: 1,
                created_at: "t".into(),
            }],
        };
        let v = serde_json::to_value(&meta).unwrap();
        assert!(v.get("value").is_none());
        assert!(v.get("versions").is_some());
    }
}
