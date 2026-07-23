//! Unit / contract / integration tests for step 10.06 — access audit + log masking.

use forge_secrets::app;
use forge_secrets::audit::recorder::{AuditEvent, AuditRecorder, AuditResult};
use forge_secrets::auth::middleware::AuthMetrics;
use forge_secrets::auth::{AuthzDecision, FakeIdentityClient, IntrospectResult};
use forge_secrets::crypto::AeadAlg;
use forge_secrets::masking::{mask_text, KnownSecrets, DEFAULT_PLACEHOLDER};
use forge_secrets::state::{bootstrap, AppState};
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
        secret_resolves_total: Arc::new(AtomicU64::new(0)),
        config_values_total: Arc::new(AtomicU64::new(0)),
        crypto_ok: false,
        crypto_error: Some("test".into()),
        auth_mode: auth_mode.into(),
        identity,
        auth_metrics: AuthMetrics::new(),
        audit_enabled: true,
        audit_strict: false,
        audit_metrics: forge_secrets::audit::recorder::AuditMetrics::new(),
        log_masking_enabled: true,
        mask_placeholder: DEFAULT_PLACEHOLDER.into(),
        known_secrets: Arc::new(KnownSecrets::new()),
    }
}

#[test]
fn openapi_declares_audit_get() {
    let doc = openapi_yaml();
    assert!(doc.contains("operationId: listAuditEvents"));
    assert!(doc.contains("/v1/projects/{project_id}/audit"));
    assert!(doc.contains("/envs/{environment}/audit") || doc.contains("/audit"));
    let start = doc.find("AuditEvent:").expect("AuditEvent schema");
    let block = &doc[start..start + 500];
    assert!(
        !block.contains("\n        value:"),
        "AuditEvent must not declare a value property"
    );
}

#[test]
fn masking_filter_replaces_known_value_mid_string() {
    let known = vec!["pw-secret".into()];
    let out = mask_text("echo attempt pw-secret in log", &known, "***");
    assert!(out.contains("***"));
    assert!(!out.contains("pw-secret"));
    assert!(out.contains("echo attempt"));
}

#[test]
fn masking_never_emits_original_value() {
    let ks = KnownSecrets::new();
    ks.register("super-secret-value-99");
    let snap = ks.snapshot();
    let out = mask_text(
        r#"{"msg":"got super-secret-value-99"}"#,
        &snap,
        DEFAULT_PLACEHOLDER,
    );
    assert!(!out.contains("super-secret-value-99"));
    assert!(out.contains(DEFAULT_PLACEHOLDER));
}

#[test]
fn audit_recorder_event_has_no_value_field() {
    let event = AuditEvent {
        project_id: "prj_1".into(),
        environment: Some("production".into()),
        action: "secret.access".into(),
        principal: "user:u1".into(),
        name: Some("DATABASE_PASSWORD".into()),
        version: Some(1),
        result: AuditResult::Denied,
        source: Some("test".into()),
    };
    assert_eq!(event.result.as_str(), "denied");
    // Structural guarantee used by persistence layer.
    let debug = format!("{event:?}");
    assert!(!debug.contains("value:"));
}

#[tokio::test]
async fn cross_project_audit_query_returns_403() {
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
    fake.stub_authz(
        "user",
        "usr-b",
        "prj_1",
        "secret.read",
        AuthzDecision {
            allow: true,
            role: "developer".into(),
            reason: "stub".into(),
        },
    );
    let state = test_state("enforce", Some(fake));
    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/projects/prj_1/envs/production/audit")
        .header("authorization", "Bearer tok-b")
        .body(axum::body::Body::empty())
        .unwrap();
    let response = app(state).oneshot(req).await.unwrap();
    assert_eq!(response.status().as_u16(), 403);
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

fn chrono_ts() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as u64
}

#[tokio::test]
#[ignore = "requires Postgres + master key; run via make test-integration or cargo test -- --ignored"]
async fn set_access_rotate_audit_trail_has_no_values() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let state = bootstrap(&cfg).await;
    assert!(state.is_ready());

    let project = format!("prj_audit_{}", chrono_ts());
    let env_name = "production";
    let secret = "DATABASE_PASSWORD";
    let value = format!("pw-secret-{}", chrono_ts());

    let app = app(state.clone());

    let set = axum::http::Request::builder()
        .method("PUT")
        .uri(format!(
            "/v1/projects/{project}/envs/{env_name}/secrets/{secret}"
        ))
        .header("content-type", "application/json")
        .body(axum::body::Body::from(format!(r#"{{"value":"{value}"}}"#)))
        .unwrap();
    let set_resp = app.clone().oneshot(set).await.unwrap();
    assert_eq!(set_resp.status().as_u16(), 201);

    let access = axum::http::Request::builder()
        .method("POST")
        .uri(format!(
            "/v1/projects/{project}/envs/{env_name}/secrets/{secret}:access"
        ))
        .body(axum::body::Body::empty())
        .unwrap();
    let access_resp = app.clone().oneshot(access).await.unwrap();
    assert_eq!(access_resp.status().as_u16(), 200);

    let rotate = axum::http::Request::builder()
        .method("PUT")
        .uri(format!(
            "/v1/projects/{project}/envs/{env_name}/secrets/{secret}"
        ))
        .header("content-type", "application/json")
        .body(axum::body::Body::from(format!(
            r#"{{"value":"{value}-rotated"}}"#
        )))
        .unwrap();
    let rotate_resp = app.clone().oneshot(rotate).await.unwrap();
    assert_eq!(rotate_resp.status().as_u16(), 201);

    let audit_req = axum::http::Request::builder()
        .method("GET")
        .uri(format!(
            "/v1/projects/{project}/envs/{env_name}/audit?name={secret}"
        ))
        .body(axum::body::Body::empty())
        .unwrap();
    let audit_resp = app.oneshot(audit_req).await.unwrap();
    assert_eq!(audit_resp.status().as_u16(), 200);
    let bytes = audit_resp.into_body().collect().await.unwrap().to_bytes();
    let body: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    let arr = body.as_array().expect("audit array");
    assert!(arr.len() >= 3, "expected set/access/rotate audit rows");
    let mut actions: Vec<&str> = arr
        .iter()
        .filter_map(|e| e.get("action").and_then(|a| a.as_str()))
        .collect();
    actions.sort();
    assert!(actions.contains(&"secret.set"));
    assert!(actions.contains(&"secret.access"));
    assert!(actions.contains(&"secret.rotate"));
    for item in arr {
        assert!(
            item.get("value").is_none(),
            "audit event must not contain value: {item}"
        );
        let s = item.to_string();
        assert!(
            !s.contains(&value),
            "audit JSON must not contain plaintext secret"
        );
    }

    // Masking: a line that would contain the secret is redacted.
    let known = state.known_secrets.snapshot();
    assert!(
        known.iter().any(|v| v == &value || v.contains("rotated")),
        "known secrets should include set values"
    );
    let masked = mask_text(&format!("echo {value}"), &known, "***");
    assert!(!masked.contains(&value));
    assert!(masked.contains("***"));
}

#[tokio::test]
#[ignore = "requires Postgres + master key"]
async fn denied_secret_access_recorded() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let mut state = bootstrap(&cfg).await;
    assert!(state.is_ready());

    let fake = Arc::new(FakeIdentityClient::new());
    fake.stub_introspect(
        "tok-deny",
        IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("usr-deny".into()),
            user_id: None,
            project_id: Some("prj_ok".into()),
            role: Some("viewer".into()),
            memberships: None,
        },
    );
    fake.stub_authz(
        "user",
        "usr-deny",
        "prj_ok",
        "secret.read",
        AuthzDecision {
            allow: false,
            role: "viewer".into(),
            reason: "denied".into(),
        },
    );
    state.auth_mode = "enforce".into();
    state.identity = Some(fake);

    let project = "prj_ok";
    let req = axum::http::Request::builder()
        .method("POST")
        .uri(format!(
            "/v1/projects/{project}/envs/production/secrets/DATABASE_PASSWORD:access"
        ))
        .header("authorization", "Bearer tok-deny")
        .body(axum::body::Body::empty())
        .unwrap();
    let response = app(state.clone()).oneshot(req).await.unwrap();
    assert_eq!(response.status().as_u16(), 403);

    // Allow query with a different principal that can read (dev bypass for query).
    let recorder = AuditRecorder::new(state.pool.clone(), true, false, state.audit_metrics.clone());
    let rows = recorder
        .query(
            project,
            Some("production"),
            Some("DATABASE_PASSWORD"),
            Some("secret.access"),
            None,
            50,
        )
        .await
        .expect("query");
    assert!(
        rows.iter().any(|r| r.result == "denied"),
        "expected denied audit row, got {rows:?}"
    );
}
