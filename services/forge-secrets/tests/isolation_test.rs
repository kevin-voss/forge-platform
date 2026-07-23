//! Unit / HTTP tests for step 10.03 — config vs secrets + project isolation.

use forge_secrets::app;
use forge_secrets::auth::middleware::{isolation_allows, AuthMetrics};
use forge_secrets::auth::{
    map_action, AuthAction, AuthTarget, AuthzDecision, FakeIdentityClient, IntrospectResult,
};
use forge_secrets::crypto::AeadAlg;
use forge_secrets::state::AppState;
use http_body_util::BodyExt;
use std::sync::atomic::{AtomicBool, AtomicU64};
use std::sync::Arc;
use std::time::Instant;
use tower::ServiceExt;

fn openapi_yaml() -> &'static str {
    include_str!("../../../contracts/openapi/forge-secrets.openapi.yaml")
}

fn test_state(
    auth_mode: &str,
    identity: Option<Arc<dyn forge_secrets::auth::IdentityClient>>,
) -> AppState {
    AppState {
        service_name: "forge-secrets".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        pool: None,
        key_provider: None,
        master_key_id: "m1".into(),
        aead_alg: AeadAlg::Aes256Gcm,
        max_value_bytes: 65536,
        ready: Arc::new(AtomicBool::new(false)),
        data_keys_total: Arc::new(AtomicU64::new(0)),
        secrets_total: Arc::new(AtomicU64::new(0)),
        secret_access_total: Arc::new(AtomicU64::new(0)),
        config_values_total: Arc::new(AtomicU64::new(0)),
        crypto_ok: false,
        crypto_error: Some("test".into()),
        auth_mode: auth_mode.into(),
        identity,
        auth_metrics: AuthMetrics::new(),
    }
}

async fn status_of(state: AppState, method: &str, uri: &str, auth: Option<&str>) -> u16 {
    let mut builder = axum::http::Request::builder().method(method).uri(uri);
    if let Some(h) = auth {
        builder = builder.header("authorization", h);
    }
    let req = builder.body(axum::body::Body::empty()).unwrap();
    let response = app(state).oneshot(req).await.unwrap();
    response.status().as_u16()
}

#[test]
fn action_mapping_unit() {
    assert!(matches!(
        map_action("GET", "/v1/projects/p/envs/e/config"),
        AuthTarget::Authorize {
            action: AuthAction::ConfigRead,
            ..
        }
    ));
    assert!(matches!(
        map_action("POST", "/v1/projects/p/envs/e/secrets/X:access"),
        AuthTarget::Authorize {
            action: AuthAction::SecretRead,
            ..
        }
    ));
    assert!(matches!(
        map_action("PUT", "/v1/projects/p/envs/e/secrets/X"),
        AuthTarget::Authorize {
            action: AuthAction::SecretWrite,
            ..
        }
    ));
}

#[test]
fn isolation_guard_rejects_cross_project_token() {
    let intro = IntrospectResult {
        active: true,
        principal_type: Some("user".into()),
        principal_id: Some("u-a".into()),
        user_id: None,
        project_id: Some("prj_a".into()),
        role: Some("developer".into()),
        memberships: None,
    };
    assert!(!isolation_allows(&intro, "prj_b"));
    assert!(isolation_allows(&intro, "prj_a"));
}

#[test]
fn openapi_declares_config_and_auth() {
    let doc = openapi_yaml();
    assert!(doc.contains("/v1/projects/{project_id}/envs/{environment}/config"));
    assert!(doc.contains("operationId: setConfig"));
    assert!(doc.contains("operationId: listConfig"));
    assert!(doc.contains("operationId: deleteConfig"));
    assert!(doc.contains("bearerAuth") || doc.contains("Bearer"));
    assert!(doc.contains("401") || doc.contains("\"401\""));
    assert!(doc.contains("403") || doc.contains("\"403\""));
    // Config list includes value; secret list does not.
    let cfg_start = doc.find("ConfigListItem:").expect("ConfigListItem");
    let cfg_block = &doc[cfg_start..cfg_start + 400];
    assert!(cfg_block.contains("value:"));
}

#[tokio::test]
async fn unauthenticated_secret_list_returns_401() {
    let fake = FakeIdentityClient::new();
    let state = test_state("enforce", Some(fake.into_arc()));
    let code = status_of(
        state,
        "GET",
        "/v1/projects/prj_1/envs/production/secrets",
        None,
    )
    .await;
    assert_eq!(code, 401);
}

#[tokio::test]
async fn cross_project_token_returns_403() {
    let fake = Arc::new(FakeIdentityClient::new());
    fake.stub_introspect(
        "tok-b",
        IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("usr-b".into()),
            user_id: None,
            project_id: Some("prj_b".into()),
            role: Some("developer".into()),
            memberships: None,
        },
    );
    // Even if authz would allow, isolation must reject first.
    fake.stub_authz(
        "user",
        "usr-b",
        "prj_1",
        "secret.read",
        AuthzDecision {
            allow: true,
            role: "developer".into(),
            reason: "should not reach".into(),
        },
    );
    let state = test_state("enforce", Some(fake.clone()));
    let code = status_of(
        state,
        "GET",
        "/v1/projects/prj_1/envs/production/secrets",
        Some("Bearer tok-b"),
    )
    .await;
    assert_eq!(code, 403);
}

#[tokio::test]
async fn viewer_config_read_allowed_secret_reveal_forbidden() {
    let fake = Arc::new(FakeIdentityClient::new());
    fake.stub_introspect(
        "viewer",
        IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("usr-view".into()),
            user_id: Some("usr-view".into()),
            project_id: Some("prj_1".into()),
            role: Some("viewer".into()),
            memberships: None,
        },
    );
    fake.stub_authz(
        "user",
        "usr-view",
        "prj_1",
        "config.read",
        AuthzDecision {
            allow: true,
            role: "viewer".into(),
            reason: "viewer may config.read".into(),
        },
    );
    fake.stub_authz(
        "user",
        "usr-view",
        "prj_1",
        "secret.read",
        AuthzDecision {
            allow: false,
            role: "viewer".into(),
            reason: "viewer may not secret.read".into(),
        },
    );

    // Config GET: auth passes → 503 not_ready (no DB) proves authz allowed.
    let state = test_state("enforce", Some(fake.clone()));
    let code = status_of(
        state.clone(),
        "GET",
        "/v1/projects/prj_1/envs/production/config",
        Some("Bearer viewer"),
    )
    .await;
    assert_eq!(code, 503);

    let code = status_of(
        state,
        "POST",
        "/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD:access",
        Some("Bearer viewer"),
    )
    .await;
    assert_eq!(code, 403);
}

#[tokio::test]
async fn developer_can_secret_and_config() {
    let fake = Arc::new(FakeIdentityClient::new());
    fake.stub_introspect(
        "dev",
        IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("usr-dev".into()),
            user_id: None,
            project_id: Some("prj_1".into()),
            role: Some("developer".into()),
            memberships: None,
        },
    );
    for action in ["secret.read", "secret.write", "config.read", "config.write"] {
        fake.stub_authz(
            "user",
            "usr-dev",
            "prj_1",
            action,
            AuthzDecision {
                allow: true,
                role: "developer".into(),
                reason: "ok".into(),
            },
        );
    }
    let state = test_state("enforce", Some(fake));
    // Auth passes → handler returns not_ready (no pool).
    assert_eq!(
        status_of(
            state.clone(),
            "GET",
            "/v1/projects/prj_1/envs/production/config",
            Some("Bearer dev"),
        )
        .await,
        503
    );
    assert_eq!(
        status_of(
            state,
            "POST",
            "/v1/projects/prj_1/envs/production/secrets/X:access",
            Some("Bearer dev"),
        )
        .await,
        503
    );
}

#[tokio::test]
async fn identity_down_secret_write_returns_503() {
    let fake = Arc::new(FakeIdentityClient::new());
    fake.set_unreachable(true);
    let state = test_state("enforce", Some(fake));
    let req = axum::http::Request::builder()
        .method("PUT")
        .uri("/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD")
        .header("authorization", "Bearer any")
        .header("content-type", "application/json")
        .body(axum::body::Body::from(r#"{"value":"x"}"#))
        .unwrap();
    let response = app(state).oneshot(req).await.unwrap();
    assert_eq!(response.status().as_u16(), 503);
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    let v: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(v["code"], "identity_unavailable");
}

#[tokio::test]
async fn identity_down_config_read_served_from_cache() {
    let fake = Arc::new(FakeIdentityClient::new());
    let intro = IntrospectResult {
        active: true,
        principal_type: Some("user".into()),
        principal_id: Some("usr-dev".into()),
        user_id: None,
        project_id: Some("prj_1".into()),
        role: Some("developer".into()),
        memberships: None,
    };
    fake.seed_introspect_cache("cached", intro);
    fake.seed_authz_cache(
        "user",
        "usr-dev",
        "prj_1",
        "config.read",
        AuthzDecision {
            allow: true,
            role: "developer".into(),
            reason: "cached".into(),
        },
    );
    fake.set_unreachable(true);
    let state = test_state("enforce", Some(fake));
    // Cache hit → proceeds to handler → 503 not_ready (no DB), not identity_unavailable.
    let code = status_of(
        state,
        "GET",
        "/v1/projects/prj_1/envs/production/config",
        Some("Bearer cached"),
    )
    .await;
    assert_eq!(code, 503);
}

#[tokio::test]
async fn health_skips_auth() {
    let fake = FakeIdentityClient::new();
    fake.set_unreachable(true);
    let state = test_state("enforce", Some(fake.into_arc()));
    let code = status_of(state, "GET", "/health/live", None).await;
    assert_eq!(code, 200);
}

#[tokio::test]
async fn dev_mode_bypasses_auth() {
    let fake = FakeIdentityClient::new();
    // No stubs — enforce would 401; dev bypasses.
    let state = test_state("dev", Some(fake.into_arc()));
    let code = status_of(
        state,
        "GET",
        "/v1/projects/prj_1/envs/production/secrets",
        None,
    )
    .await;
    // Handler runs → not ready
    assert_eq!(code, 503);
}

fn db_url() -> Option<String> {
    std::env::var("FORGE_SECRETS_DB_URL")
        .ok()
        .filter(|s| !s.trim().is_empty())
}

fn master_key_configured() -> bool {
    std::env::var("FORGE_SECRETS_MASTER_KEY")
        .ok()
        .filter(|s| !s.trim().is_empty())
        .is_some()
}

#[tokio::test]
#[ignore = "requires Postgres + master key; run via make test-integration"]
async fn config_list_returns_values_secret_list_hides() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    // Force dev auth so store tests focus on config vs secrets payloads.
    std::env::set_var("FORGE_AUTH_MODE", "dev");
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let state = forge_secrets::state::bootstrap(&cfg).await;
    assert!(state.is_ready());

    let project = format!("prj_iso_{}", chrono_ts());
    let env_name = "production";

    let pool = state.pool.as_ref().expect("pool").clone();
    let cfg_store = forge_secrets::config_store::ConfigStore::new(pool.clone());
    cfg_store
        .upsert(&project, env_name, "FEATURE_X", "true")
        .await
        .expect("upsert config");

    let list = cfg_store.list(&project, env_name).await.expect("list");
    assert_eq!(list.len(), 1);
    assert_eq!(list[0].value, "true");

    // Secret via store: encrypt + insert; list metadata has no value field at API layer.
    let (data_key, dk_ver) = state
        .unwrap_project_data_key(&project)
        .await
        .expect("data key");
    let enc =
        forge_secrets::secrets::encrypt(state.aead_alg, &data_key, b"s3cret").expect("encrypt");
    let secret_store = forge_secrets::secrets::SecretStore::new(pool);
    secret_store
        .insert_version(&forge_secrets::secrets::NewSecretVersion {
            project_id: &project,
            environment: env_name,
            name: "DATABASE_PASSWORD",
            version: 1,
            ciphertext: &enc.ciphertext,
            nonce: &enc.nonce,
            data_key_version: dk_ver,
        })
        .await
        .expect("insert secret");
    let secrets = secret_store
        .list_metadata(&project, env_name)
        .await
        .expect("secret list");
    assert_eq!(secrets.len(), 1);
    assert_eq!(secrets[0].name, "DATABASE_PASSWORD");
}

fn chrono_ts() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs()
}
