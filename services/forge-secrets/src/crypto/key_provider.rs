use base64::Engine;
use std::env;
use std::sync::Arc;

/// Length of the AES-256 master key in bytes.
pub const MASTER_KEY_LEN: usize = 32;

/// Supplies the platform master key. Seam for future KMS/HSM providers.
pub trait KeyProvider: Send + Sync {
    fn master_key(&self) -> &[u8; MASTER_KEY_LEN];
    fn master_key_id(&self) -> &str;
}

/// Loads `FORGE_SECRETS_MASTER_KEY` (base64, 32 bytes) and optional key id.
#[derive(Debug, Clone)]
pub struct EnvMasterKeyProvider {
    key: [u8; MASTER_KEY_LEN],
    key_id: String,
}

impl EnvMasterKeyProvider {
    pub fn from_env() -> Result<Self, String> {
        let raw = env::var("FORGE_SECRETS_MASTER_KEY").map_err(|_| {
            "FORGE_SECRETS_MASTER_KEY is required (base64-encoded 32-byte key)".to_string()
        })?;
        let key_id = env::var("FORGE_SECRETS_MASTER_KEY_ID")
            .unwrap_or_else(|_| "m1".into())
            .trim()
            .to_string();
        let key_id = if key_id.is_empty() {
            "m1".into()
        } else {
            key_id
        };
        Self::from_base64(raw.trim(), key_id)
    }

    pub fn from_base64(encoded: &str, key_id: impl Into<String>) -> Result<Self, String> {
        if encoded.is_empty() {
            return Err("FORGE_SECRETS_MASTER_KEY must not be empty".into());
        }
        let decoded = base64::engine::general_purpose::STANDARD
            .decode(encoded)
            .map_err(|_| "FORGE_SECRETS_MASTER_KEY must be valid standard base64".to_string())?;
        if decoded.len() != MASTER_KEY_LEN {
            return Err(format!(
                "FORGE_SECRETS_MASTER_KEY must decode to {MASTER_KEY_LEN} bytes, got {}",
                decoded.len()
            ));
        }
        let mut key = [0u8; MASTER_KEY_LEN];
        key.copy_from_slice(&decoded);
        Ok(Self {
            key,
            key_id: key_id.into(),
        })
    }

    pub fn into_arc(self) -> Arc<dyn KeyProvider> {
        Arc::new(self)
    }
}

impl KeyProvider for EnvMasterKeyProvider {
    fn master_key(&self) -> &[u8; MASTER_KEY_LEN] {
        &self.key
    }

    fn master_key_id(&self) -> &str {
        &self.key_id
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    static ENV_LOCK: Mutex<()> = Mutex::new(());

    fn with_env<F>(vars: &[(&str, Option<&str>)], f: F)
    where
        F: FnOnce(),
    {
        let _guard = ENV_LOCK.lock().unwrap();
        let keys = ["FORGE_SECRETS_MASTER_KEY", "FORGE_SECRETS_MASTER_KEY_ID"];
        let previous: Vec<(String, Option<String>)> = keys
            .iter()
            .map(|k| ((*k).to_string(), env::var(k).ok()))
            .collect();
        for k in keys {
            // SAFETY: serialized by ENV_LOCK for unit tests only.
            unsafe { env::remove_var(k) };
        }
        for (k, v) in vars {
            match v {
                // SAFETY: serialized by ENV_LOCK for unit tests only.
                Some(val) => unsafe { env::set_var(k, val) },
                None => unsafe { env::remove_var(k) },
            }
        }
        f();
        for (k, v) in previous {
            match v {
                // SAFETY: serialized by ENV_LOCK for unit tests only.
                Some(val) => unsafe { env::set_var(&k, val) },
                None => unsafe { env::remove_var(&k) },
            }
        }
    }

    fn valid_b64() -> String {
        base64::engine::general_purpose::STANDARD.encode([7u8; MASTER_KEY_LEN])
    }

    #[test]
    fn rejects_missing_key() {
        with_env(&[("FORGE_SECRETS_MASTER_KEY", None)], || {
            let err = EnvMasterKeyProvider::from_env().unwrap_err();
            assert!(err.contains("required"));
        });
    }

    #[test]
    fn rejects_short_key() {
        let short = base64::engine::general_purpose::STANDARD.encode([1u8; 16]);
        with_env(
            &[("FORGE_SECRETS_MASTER_KEY", Some(short.as_str()))],
            || {
                let err = EnvMasterKeyProvider::from_env().unwrap_err();
                assert!(err.contains("32 bytes"));
            },
        );
    }

    #[test]
    fn accepts_valid_32_byte_base64_key() {
        let b64 = valid_b64();
        with_env(
            &[
                ("FORGE_SECRETS_MASTER_KEY", Some(b64.as_str())),
                ("FORGE_SECRETS_MASTER_KEY_ID", Some("m-test")),
            ],
            || {
                let provider = EnvMasterKeyProvider::from_env().expect("provider");
                assert_eq!(provider.master_key(), &[7u8; MASTER_KEY_LEN]);
                assert_eq!(provider.master_key_id(), "m-test");
            },
        );
    }
}
