use crate::bindings::store::{BindingRow, BindingStore};
use crate::config_store::store::ConfigStore;
use crate::crypto::aead_alg::AeadAlg;
use crate::crypto::data_key::DataKey;
use crate::secrets::cipher::decrypt;
use crate::secrets::store::SecretStore;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::collections::BTreeMap;
use std::fmt::Write as _;

fn hex_encode(bytes: impl AsRef<[u8]>) -> String {
    bytes.as_ref().iter().map(|b| format!("{b:02x}")).collect()
}

/// Resolved env bundle for Runtime injection (plaintext transient only).
#[derive(Debug, Clone)]
pub struct ResolveBundle {
    pub env: BTreeMap<String, String>,
    pub version_fingerprint: String,
    pub keys: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MissingBinding {
    pub kind: &'static str,
    pub name: String,
}

#[derive(Debug)]
pub enum ResolveError {
    Storage(String),
    Crypto(String),
    Missing(Vec<MissingBinding>),
}

/// Hash over (secret name→version, config name→updated_at) for redeploy detection.
pub fn fingerprint_for(
    secret_versions: &[(String, i32)],
    config_updated: &[(String, String)],
) -> String {
    let mut material = String::new();
    for (name, version) in secret_versions {
        let _ = writeln!(material, "s:{name}={version}");
    }
    for (name, updated_at) in config_updated {
        let _ = writeln!(material, "c:{name}={updated_at}");
    }
    let digest = Sha256::digest(material.as_bytes());
    hex_encode(digest)
}

/// Combine decrypted secrets + config for a service binding into one env map.
pub async fn resolve_bundle(
    pool: &PgPool,
    aead_alg: AeadAlg,
    data_key: &DataKey,
    project_id: &str,
    environment: &str,
    binding: &BindingRow,
) -> Result<ResolveBundle, ResolveError> {
    let secret_store = SecretStore::new(pool.clone());
    let config_store = ConfigStore::new(pool.clone());

    let mut env = BTreeMap::new();
    let mut secret_versions: Vec<(String, i32)> = Vec::new();
    let mut config_updated: Vec<(String, String)> = Vec::new();
    let mut missing: Vec<MissingBinding> = Vec::new();

    for name in &binding.secret_names {
        match secret_store
            .fetch_for_decrypt(project_id, environment, name, None)
            .await
        {
            Ok(None) => missing.push(MissingBinding {
                kind: "secret",
                name: name.clone(),
            }),
            Ok(Some(row)) => {
                let plaintext = decrypt(aead_alg, data_key, &row.ciphertext, &row.nonce)
                    .map_err(ResolveError::Crypto)?;
                let value = String::from_utf8(plaintext).map_err(|_| {
                    ResolveError::Crypto(format!("secret {name} decrypt produced non-utf8"))
                })?;
                secret_versions.push((name.clone(), row.version));
                env.insert(name.clone(), value);
            }
            Err(err) => return Err(ResolveError::Storage(err)),
        }
    }

    for name in &binding.config_names {
        match config_store.get(project_id, environment, name).await {
            Ok(None) => missing.push(MissingBinding {
                kind: "config",
                name: name.clone(),
            }),
            Ok(Some(row)) => {
                config_updated.push((name.clone(), row.updated_at.to_rfc3339()));
                env.insert(name.clone(), row.value);
            }
            Err(err) => return Err(ResolveError::Storage(err)),
        }
    }

    if !missing.is_empty() {
        return Err(ResolveError::Missing(missing));
    }

    secret_versions.sort_by(|a, b| a.0.cmp(&b.0));
    config_updated.sort_by(|a, b| a.0.cmp(&b.0));
    let version_fingerprint = fingerprint_for(&secret_versions, &config_updated);
    let keys: Vec<String> = env.keys().cloned().collect();

    Ok(ResolveBundle {
        env,
        version_fingerprint,
        keys,
    })
}

/// Load binding (empty if none) then resolve.
pub async fn resolve_for_service(
    pool: &PgPool,
    aead_alg: AeadAlg,
    data_key: &DataKey,
    project_id: &str,
    environment: &str,
    service: &str,
) -> Result<ResolveBundle, ResolveError> {
    let store = BindingStore::new(pool.clone());
    let binding = store
        .get(project_id, environment, service)
        .await
        .map_err(ResolveError::Storage)?
        .unwrap_or(BindingRow {
            project_id: project_id.to_string(),
            environment: environment.to_string(),
            service: service.to_string(),
            secret_names: vec![],
            config_names: vec![],
            updated_at: chrono::Utc::now(),
        });
    resolve_bundle(pool, aead_alg, data_key, project_id, environment, &binding).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fingerprint_changes_when_secret_version_changes() {
        let a = fingerprint_for(&[("DATABASE_PASSWORD".into(), 1)], &[]);
        let b = fingerprint_for(&[("DATABASE_PASSWORD".into(), 2)], &[]);
        assert_ne!(a, b);
        assert_eq!(fingerprint_for(&[("DATABASE_PASSWORD".into(), 1)], &[]), a);
    }

    #[test]
    fn fingerprint_includes_config_updated_at() {
        let a = fingerprint_for(&[], &[("FEATURE_X".into(), "t1".into())]);
        let b = fingerprint_for(&[], &[("FEATURE_X".into(), "t2".into())]);
        assert_ne!(a, b);
    }

    #[test]
    fn empty_binding_fingerprint_stable() {
        let a = fingerprint_for(&[], &[]);
        let b = fingerprint_for(&[], &[]);
        assert_eq!(a, b);
        assert!(!a.is_empty());
    }
}
