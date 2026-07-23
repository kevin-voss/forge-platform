use crate::crypto::aead_alg::AeadAlg;
use std::env;
use std::time::Duration;

/// Env-backed configuration for forge-secrets.
#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
    pub shutdown_grace: Duration,
    pub db_url: String,
    pub master_key_id: String,
    /// Present when `FORGE_SECRETS_MASTER_KEY` is set (may still be invalid length/base64).
    pub master_key_b64: Option<String>,
    pub aead_alg: AeadAlg,
    pub max_value_bytes: usize,
    /// `enforce` (default) or `dev` (insecure bypass).
    pub auth_mode: String,
    pub identity_url: String,
    pub introspect_cache_ttl_s: u64,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let port_raw = env::var("PORT").unwrap_or_else(|_| "4104".into());
        let port_raw = port_raw.trim();
        if port_raw.is_empty() {
            return Err("PORT is required".into());
        }
        let port: u16 = port_raw
            .parse()
            .map_err(|_| format!("PORT must be an integer 1–65535, got {port_raw:?}"))?;
        if port == 0 {
            return Err(format!("PORT must be an integer 1–65535, got {port_raw:?}"));
        }

        let log_level = env::var("FORGE_LOG_LEVEL")
            .unwrap_or_else(|_| "info".into())
            .trim()
            .to_ascii_lowercase();
        match log_level.as_str() {
            "debug" | "info" | "warn" | "error" => {}
            other => {
                return Err(format!(
                    "FORGE_LOG_LEVEL must be debug|info|warn|error, got {other:?}"
                ));
            }
        }

        let service_name = non_empty_env("FORGE_SERVICE_NAME", "forge-secrets");
        let service_version = non_empty_env("FORGE_SERVICE_VERSION", "0.1.0");
        let env_name = non_empty_env("FORGE_ENV", "development");

        let grace_raw = env::var("FORGE_SHUTDOWN_GRACE_SECONDS").unwrap_or_else(|_| "10".into());
        let grace_secs: u64 = grace_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got {grace_raw:?}"
            )
        })?;

        let db_url = env::var("FORGE_SECRETS_DB_URL")
            .unwrap_or_else(|_| "postgres://forge:forge@127.0.0.1:5001/forge_secrets".into());
        let db_url = db_url.trim().to_string();
        if db_url.is_empty() {
            return Err("FORGE_SECRETS_DB_URL must not be empty".into());
        }

        let master_key_id = non_empty_env("FORGE_SECRETS_MASTER_KEY_ID", "m1");
        let master_key_b64 = env::var("FORGE_SECRETS_MASTER_KEY")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let aead_raw = env::var("FORGE_SECRETS_AEAD_ALG").unwrap_or_else(|_| "aes-256-gcm".into());
        let aead_alg = AeadAlg::parse(&aead_raw)?;

        let max_raw = env::var("FORGE_SECRETS_MAX_VALUE_BYTES").unwrap_or_else(|_| "65536".into());
        let max_value_bytes: usize = max_raw.trim().parse().map_err(|_| {
            format!("FORGE_SECRETS_MAX_VALUE_BYTES must be a positive integer, got {max_raw:?}")
        })?;
        if max_value_bytes == 0 {
            return Err("FORGE_SECRETS_MAX_VALUE_BYTES must be > 0".into());
        }

        let auth_mode = env::var("FORGE_AUTH_MODE")
            .unwrap_or_else(|_| "enforce".into())
            .trim()
            .to_ascii_lowercase();
        match auth_mode.as_str() {
            "enforce" | "dev" => {}
            other => {
                return Err(format!(
                    "FORGE_AUTH_MODE must be enforce|dev, got {other:?}"
                ));
            }
        }

        let identity_url = non_empty_env("FORGE_IDENTITY_URL", "http://forge-identity:4002");

        let ttl_raw = env::var("FORGE_INTROSPECT_CACHE_TTL_S").unwrap_or_else(|_| "10".into());
        let introspect_cache_ttl_s: u64 = ttl_raw.trim().parse().map_err(|_| {
            format!("FORGE_INTROSPECT_CACHE_TTL_S must be a non-negative integer, got {ttl_raw:?}")
        })?;

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
            shutdown_grace: Duration::from_secs(grace_secs),
            db_url,
            master_key_id,
            master_key_b64,
            aead_alg,
            max_value_bytes,
            auth_mode,
            identity_url,
            introspect_cache_ttl_s,
        })
    }
}

fn non_empty_env(key: &str, default: &str) -> String {
    let value = env::var(key).unwrap_or_else(|_| default.into());
    let trimmed = value.trim();
    if trimmed.is_empty() {
        default.into()
    } else {
        trimmed.to_string()
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
        let keys = [
            "PORT",
            "FORGE_SERVICE_NAME",
            "FORGE_SERVICE_VERSION",
            "FORGE_LOG_LEVEL",
            "FORGE_ENV",
            "FORGE_SHUTDOWN_GRACE_SECONDS",
            "FORGE_SECRETS_DB_URL",
            "FORGE_SECRETS_MASTER_KEY",
            "FORGE_SECRETS_MASTER_KEY_ID",
            "FORGE_SECRETS_AEAD_ALG",
            "FORGE_SECRETS_MAX_VALUE_BYTES",
            "FORGE_AUTH_MODE",
            "FORGE_IDENTITY_URL",
            "FORGE_INTROSPECT_CACHE_TTL_S",
        ];
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

    #[test]
    fn defaults_port_to_4104() {
        with_env(
            &[
                ("PORT", None),
                ("FORGE_LOG_LEVEL", Some("info")),
                ("FORGE_SECRETS_DB_URL", Some("postgres://x")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.port, 4104);
                assert_eq!(cfg.service_name, "forge-secrets");
                assert_eq!(cfg.master_key_id, "m1");
                assert!(cfg.master_key_b64.is_none());
                assert_eq!(cfg.auth_mode, "enforce");
                assert_eq!(cfg.identity_url, "http://forge-identity:4002");
                assert_eq!(cfg.introspect_cache_ttl_s, 10);
            },
        );
    }
}
