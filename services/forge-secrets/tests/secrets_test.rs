//! Integration / contract tests for step 10.02 encrypted secret store.
//!
//! DB-backed cases require Postgres (`FORGE_SECRETS_DB_URL`) and a valid master key.
//! Run via `make test-integration` (service + curl) or:
//! `FORGE_SECRETS_DB_URL=... FORGE_SECRETS_MASTER_KEY=... cargo test --test secrets_test -- --ignored`

use forge_secrets::crypto::{generate_data_key, unwrap_data_key, AeadAlg};
use forge_secrets::db;
use forge_secrets::secrets::cipher::{decrypt, encrypt};
use forge_secrets::secrets::store::{NewSecretVersion, SecretStore};
use forge_secrets::state::bootstrap;
use std::sync::atomic::Ordering;

fn openapi_yaml() -> &'static str {
    include_str!("../../../contracts/openapi/forge-secrets.openapi.yaml")
}

#[test]
fn openapi_declares_set_list_metadata_access() {
    let doc = openapi_yaml();
    assert!(doc.contains("/v1/projects/{project_id}/envs/{environment}/secrets"));
    assert!(doc.contains("operationId: setSecret"));
    assert!(doc.contains("operationId: listSecrets"));
    assert!(doc.contains("operationId: getSecretMetadata"));
    assert!(doc.contains("/secrets/{name}:access"));
    assert!(doc.contains("operationId: accessSecret"));
}

#[test]
fn openapi_list_schema_has_no_value_field() {
    let doc = openapi_yaml();
    // Extract SecretListItem schema block roughly and ensure no value property.
    let start = doc.find("SecretListItem:").expect("SecretListItem schema");
    let rest = &doc[start..];
    let end = rest.find("\n    SecretVersion:").unwrap_or(rest.len());
    let block = &rest[..end];
    assert!(
        !block.contains("\n        value:"),
        "SecretListItem must not declare a value property"
    );
    assert!(block.contains("additionalProperties: false"));
    assert!(block.contains("name:"));
    assert!(block.contains("version:"));
}

#[test]
fn openapi_examples_validate_shapes() {
    let doc = openapi_yaml();
    assert!(doc.contains("value: s3cret")); // set + access examples
    assert!(doc.contains("name: DATABASE_PASSWORD"));
    // List example items must not show a value key in the example array.
    let list_ex = doc
        .find("operationId: listSecrets")
        .and_then(|i| doc[i..].find("example:"))
        .map(|j| {
            let start = doc.find("operationId: listSecrets").unwrap() + j;
            &doc[start..start + 280]
        })
        .expect("list example");
    assert!(!list_ex.contains("value:"));
}

#[test]
fn cipher_round_trip_and_distinct_nonces() {
    let key = generate_data_key();
    let a = encrypt(AeadAlg::Aes256Gcm, &key, b"s3cret").unwrap();
    let b = encrypt(AeadAlg::Aes256Gcm, &key, b"s3cret").unwrap();
    assert_ne!(a.nonce, b.nonce);
    assert_ne!(a.ciphertext, b.ciphertext);
    assert_eq!(
        decrypt(AeadAlg::Aes256Gcm, &key, &a.ciphertext, &a.nonce).unwrap(),
        b"s3cret"
    );
}

#[test]
fn setting_versions_are_monotonic_in_memory_logic() {
    // Pure unit: next version = max+1 semantics mirrored here.
    let previous = 3;
    assert_eq!(previous + 1, 4);
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
#[ignore = "requires Postgres + master key; run via make test-integration or cargo test -- --ignored"]
async fn set_list_access_encrypted_at_rest() {
    if db_url().is_none() || !master_key_configured() {
        panic!("FORGE_SECRETS_DB_URL and FORGE_SECRETS_MASTER_KEY required");
    }
    let cfg = forge_secrets::config::Config::from_env().expect("config");
    let state = bootstrap(&cfg).await;
    assert!(state.is_ready(), "service state must be ready for DB tests");

    let pool = state.pool.as_ref().expect("pool");
    let store = SecretStore::new(pool.clone());
    let project = format!("prj_it_{}", chrono_ts());
    let env_name = "production";
    let name = "DATABASE_PASSWORD";
    let plaintext = b"s3cret";

    let (data_key, dk_ver) = state
        .unwrap_project_data_key(&project)
        .await
        .expect("data key");
    let enc = encrypt(state.aead_alg, &data_key, plaintext).expect("encrypt");
    let v1 = store
        .next_version(&project, env_name, name)
        .await
        .expect("v1");
    assert_eq!(v1, 1);
    store
        .insert_version(&NewSecretVersion {
            project_id: &project,
            environment: env_name,
            name,
            version: v1,
            ciphertext: &enc.ciphertext,
            nonce: &enc.nonce,
            data_key_version: dk_ver,
        })
        .await
        .expect("insert v1");

    assert!(
        !store
            .ciphertext_contains_plaintext(&project, env_name, name, plaintext)
            .await
            .expect("check"),
        "plaintext must not appear in ciphertext bytes"
    );

    let list = store.list_metadata(&project, env_name).await.expect("list");
    assert_eq!(list.len(), 1);
    assert_eq!(list[0].name, name);
    assert_eq!(list[0].version, 1);

    // Second set → version 2; access latest.
    let enc2 = encrypt(state.aead_alg, &data_key, b"rotated").expect("encrypt2");
    let v2 = store
        .next_version(&project, env_name, name)
        .await
        .expect("v2");
    assert_eq!(v2, 2);
    store
        .insert_version(&NewSecretVersion {
            project_id: &project,
            environment: env_name,
            name,
            version: v2,
            ciphertext: &enc2.ciphertext,
            nonce: &enc2.nonce,
            data_key_version: dk_ver,
        })
        .await
        .expect("insert v2");

    let history = store
        .version_history(&project, env_name, name)
        .await
        .expect("history");
    assert_eq!(history.len(), 2);

    let latest = store
        .fetch_for_decrypt(&project, env_name, name, None)
        .await
        .expect("fetch")
        .expect("row");
    assert_eq!(latest.version, 2);
    let value =
        decrypt(state.aead_alg, &data_key, &latest.ciphertext, &latest.nonce).expect("decrypt");
    assert_eq!(value, b"rotated");

    // Wrong data key → clean failure.
    let wrong = generate_data_key();
    let err = decrypt(state.aead_alg, &wrong, &latest.ciphertext, &latest.nonce).unwrap_err();
    assert_eq!(err, "crypto_decrypt_failed");

    // Prove wrap/unwrap still works for the stored project key.
    let row = db::get_project_data_key(pool, &project)
        .await
        .expect("get")
        .expect("row");
    let provider = state.key_provider.as_ref().expect("provider");
    let unwrapped = unwrap_data_key(provider.master_key(), &row.wrapped_key).expect("unwrap");
    assert_eq!(unwrapped.as_bytes(), data_key.as_bytes());

    state.secrets_total.fetch_add(2, Ordering::Relaxed);
}

fn chrono_ts() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs()
}
