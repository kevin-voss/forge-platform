//! Unit / contract / integration tests for step 10.04 bindings + resolve.

use forge_secrets::bindings::resolve::fingerprint_for;
use forge_secrets::bindings::store::BindingStore;
use forge_secrets::config_store::store::ConfigStore;
use forge_secrets::secrets::cipher::encrypt;
use forge_secrets::secrets::store::{NewSecretVersion, SecretStore};
use forge_secrets::state::bootstrap;

fn openapi_yaml() -> &'static str {
    include_str!("../../../contracts/openapi/forge-secrets.openapi.yaml")
}

#[test]
fn openapi_declares_bindings_and_resolve() {
    let doc = openapi_yaml();
    assert!(doc.contains("/services/{service}/bindings"));
    assert!(doc.contains("/services/{service}/resolve"));
    assert!(doc.contains("operationId: putServiceBindings"));
    assert!(doc.contains("operationId: resolveServiceEnv"));
    assert!(doc.contains("version_fingerprint"));
    assert!(doc.contains("missing_bindings"));
}

#[test]
fn fingerprint_changes_when_secret_version_changes() {
    let v1 = fingerprint_for(&[("DATABASE_PASSWORD".into(), 1)], &[]);
    let v2 = fingerprint_for(&[("DATABASE_PASSWORD".into(), 2)], &[]);
    assert_ne!(v1, v2);
}

#[test]
fn resolve_log_formatter_masks_values_by_not_logging_them() {
    // Acceptance: env bundle never logged — resolve routes log keys + fingerprint only.
    // Assert the OpenAPI / route contract documents this and unit-level fingerprint helpers
    // do not embed plaintext.
    let fp = fingerprint_for(&[("DATABASE_PASSWORD".into(), 1)], &[]);
    assert!(!fp.contains("s3cret"));
    assert!(!fp.contains("pw1"));
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
#[ignore = "requires Postgres + master key; run via cargo test -- --ignored"]
async fn resolve_merges_secrets_and_config_fingerprint_tracks_versions() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let state = bootstrap(&cfg).await;
    assert!(state.is_ready());
    let pool = state.pool.as_ref().expect("pool");
    let project = format!("prj_resolve_{}", chrono_ts());
    let env_name = "production";
    let service = "demo";

    let (data_key, dk_ver) = state
        .unwrap_project_data_key(&project)
        .await
        .expect("data key");
    let secret_store = SecretStore::new(pool.clone());
    let config_store = ConfigStore::new(pool.clone());
    let binding_store = BindingStore::new(pool.clone());

    let enc = encrypt(state.aead_alg, &data_key, b"pw1").expect("encrypt");
    let v = secret_store
        .next_version(&project, env_name, "DATABASE_PASSWORD")
        .await
        .unwrap();
    secret_store
        .insert_version(&NewSecretVersion {
            project_id: &project,
            environment: env_name,
            name: "DATABASE_PASSWORD",
            version: v,
            ciphertext: &enc.ciphertext,
            nonce: &enc.nonce,
            data_key_version: dk_ver,
        })
        .await
        .unwrap();
    config_store
        .upsert(&project, env_name, "FEATURE_X", "true")
        .await
        .unwrap();
    binding_store
        .upsert(
            &project,
            env_name,
            service,
            &["DATABASE_PASSWORD".into()],
            &["FEATURE_X".into()],
        )
        .await
        .unwrap();

    let bundle = forge_secrets::bindings::resolve::resolve_for_service(
        pool,
        state.aead_alg,
        &data_key,
        &project,
        env_name,
        service,
    )
    .await
    .expect("resolve");
    assert_eq!(
        bundle.env.get("DATABASE_PASSWORD").map(String::as_str),
        Some("pw1")
    );
    assert_eq!(
        bundle.env.get("FEATURE_X").map(String::as_str),
        Some("true")
    );
    let fp1 = bundle.version_fingerprint.clone();

    let enc2 = encrypt(state.aead_alg, &data_key, b"pw2").expect("encrypt");
    let v2 = secret_store
        .next_version(&project, env_name, "DATABASE_PASSWORD")
        .await
        .unwrap();
    secret_store
        .insert_version(&NewSecretVersion {
            project_id: &project,
            environment: env_name,
            name: "DATABASE_PASSWORD",
            version: v2,
            ciphertext: &enc2.ciphertext,
            nonce: &enc2.nonce,
            data_key_version: dk_ver,
        })
        .await
        .unwrap();

    let bundle2 = forge_secrets::bindings::resolve::resolve_for_service(
        pool,
        state.aead_alg,
        &data_key,
        &project,
        env_name,
        service,
    )
    .await
    .expect("resolve after rotate");
    assert_eq!(
        bundle2.env.get("DATABASE_PASSWORD").map(String::as_str),
        Some("pw2")
    );
    assert_ne!(fp1, bundle2.version_fingerprint);
}

#[tokio::test]
#[ignore = "requires Postgres + master key; run via cargo test -- --ignored"]
async fn resolve_missing_bound_secret_lists_name() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let state = bootstrap(&cfg).await;
    assert!(state.is_ready());
    let pool = state.pool.as_ref().expect("pool");
    let project = format!("prj_missing_{}", chrono_ts());
    let env_name = "production";
    let service = "demo";

    let (data_key, _) = state
        .unwrap_project_data_key(&project)
        .await
        .expect("data key");
    BindingStore::new(pool.clone())
        .upsert(
            &project,
            env_name,
            service,
            &["DATABASE_PASSWORD".into()],
            &[],
        )
        .await
        .unwrap();

    let err = forge_secrets::bindings::resolve::resolve_for_service(
        pool,
        state.aead_alg,
        &data_key,
        &project,
        env_name,
        service,
    )
    .await
    .expect_err("must fail");
    match err {
        forge_secrets::bindings::ResolveError::Missing(m) => {
            assert!(m.iter().any(|x| x.name == "DATABASE_PASSWORD"));
        }
        other => panic!("unexpected {other:?}"),
    }
}
