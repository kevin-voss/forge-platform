use crate::backend::DEFAULT_STREAM_BUFFER_BYTES;
use std::env;
use std::fmt;
use std::path::{Path, PathBuf};
use std::time::Duration;

/// Authentication mode for project isolation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AuthMode {
    Dev,
    Enforce,
}

impl AuthMode {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "" | "dev" => Ok(Self::Dev),
            "enforce" | "enforced" => Ok(Self::Enforce),
            other => Err(format!(
                "FORGE_AUTH_MODE must be enforce|dev, got {other:?}"
            )),
        }
    }
}

/// On-read integrity verification mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VerifyOnRead {
    Off,
    Full,
}

impl VerifyOnRead {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "" | "off" | "false" | "0" => Ok(Self::Off),
            "full" | "on" | "true" | "1" => Ok(Self::Full),
            other => Err(format!(
                "FORGE_STORAGE_VERIFY_ON_READ must be off|full, got {other:?}"
            )),
        }
    }
}

impl fmt::Display for VerifyOnRead {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Off => write!(f, "off"),
            Self::Full => write!(f, "full"),
        }
    }
}

impl fmt::Display for AuthMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Dev => write!(f, "dev"),
            Self::Enforce => write!(f, "enforce"),
        }
    }
}

/// Env-backed configuration for forge-storage.
#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
    pub shutdown_grace: Duration,
    pub storage_root: PathBuf,
    pub allowed_base: PathBuf,
    pub meta_path: PathBuf,
    pub auth_mode: AuthMode,
    pub identity_url: Option<String>,
    pub identity_cache_ttl_secs: u64,
    pub ready_retry_initial: Duration,
    pub ready_retry_max: Duration,
    /// Fixed-size stream buffer for upload/download (default 64 KiB).
    pub stream_buffer_bytes: usize,
    /// Optional hard cap on object size; `None` means unlimited (quotas in 13.06).
    pub max_object_bytes: Option<u64>,
    /// Re-hash on-disk blobs during GET when `full` (default `off`).
    pub verify_on_read: VerifyOnRead,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let port_raw = env::var("PORT").unwrap_or_else(|_| "4107".into());
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

        let service_name = non_empty_env("FORGE_SERVICE_NAME", "forge-storage");
        let service_version = non_empty_env("FORGE_SERVICE_VERSION", "0.1.0");
        let env_name = non_empty_env("FORGE_ENV", "development");

        let grace_raw = env::var("FORGE_SHUTDOWN_GRACE_SECONDS").unwrap_or_else(|_| "10".into());
        let grace_secs: u64 = grace_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got {grace_raw:?}"
            )
        })?;

        let storage_root = PathBuf::from(
            env::var("FORGE_STORAGE_ROOT")
                .unwrap_or_else(|_| "/data/storage".into())
                .trim(),
        );
        if storage_root.as_os_str().is_empty() {
            return Err("FORGE_STORAGE_ROOT must not be empty".into());
        }

        let allowed_base = match env::var("FORGE_STORAGE_ALLOWED_BASE") {
            Ok(v) if !v.trim().is_empty() => PathBuf::from(v.trim()),
            _ => storage_root
                .parent()
                .filter(|p| !p.as_os_str().is_empty())
                .map(Path::to_path_buf)
                .unwrap_or_else(|| PathBuf::from("/")),
        };

        let meta_path = match env::var("FORGE_STORAGE_META_PATH") {
            Ok(v) if !v.trim().is_empty() => PathBuf::from(v.trim()),
            _ => storage_root.join("meta").join("index.db"),
        };

        let auth_mode =
            AuthMode::parse(&env::var("FORGE_AUTH_MODE").unwrap_or_else(|_| "dev".into()))?;

        let identity_url = env::var("FORGE_IDENTITY_URL")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        if auth_mode == AuthMode::Enforce && identity_url.is_none() {
            return Err("FORGE_IDENTITY_URL is required when FORGE_AUTH_MODE=enforce".into());
        }

        let identity_cache_ttl_secs = parse_u64_env("FORGE_IDENTITY_CACHE_TTL_SECONDS", 10)?;

        let ready_retry_initial =
            Duration::from_millis(parse_u64_env("FORGE_STORAGE_READY_RETRY_INITIAL_MS", 500)?);
        let ready_retry_max =
            Duration::from_millis(parse_u64_env("FORGE_STORAGE_READY_RETRY_MAX_MS", 10_000)?);

        let stream_buffer_bytes =
            parse_u64_env(
                "FORGE_STORAGE_STREAM_BUFFER_BYTES",
                DEFAULT_STREAM_BUFFER_BYTES as u64,
            )? as usize;
        if stream_buffer_bytes == 0 {
            return Err(
                "FORGE_STORAGE_STREAM_BUFFER_BYTES must be a positive integer".into(),
            );
        }

        let max_object_bytes = match env::var("FORGE_STORAGE_MAX_OBJECT_BYTES") {
            Ok(raw) => {
                let trimmed = raw.trim();
                if trimmed.is_empty() {
                    None
                } else {
                    Some(trimmed.parse::<u64>().map_err(|_| {
                        format!(
                            "FORGE_STORAGE_MAX_OBJECT_BYTES must be a non-negative integer, got {raw:?}"
                        )
                    })?)
                }
            }
            Err(_) => None,
        };

        let verify_on_read = VerifyOnRead::parse(
            &env::var("FORGE_STORAGE_VERIFY_ON_READ").unwrap_or_else(|_| "off".into()),
        )?;

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
            shutdown_grace: Duration::from_secs(grace_secs),
            storage_root,
            allowed_base,
            meta_path,
            auth_mode,
            identity_url,
            identity_cache_ttl_secs,
            ready_retry_initial,
            ready_retry_max,
            stream_buffer_bytes,
            max_object_bytes,
            verify_on_read,
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

fn parse_u64_env(key: &str, default: u64) -> Result<u64, String> {
    match env::var(key) {
        Ok(raw) => {
            let trimmed = raw.trim();
            if trimmed.is_empty() {
                return Ok(default);
            }
            trimmed
                .parse()
                .map_err(|_| format!("{key} must be a non-negative integer, got {raw:?}"))
        }
        Err(_) => Ok(default),
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
            "FORGE_STORAGE_ROOT",
            "FORGE_STORAGE_ALLOWED_BASE",
            "FORGE_STORAGE_META_PATH",
            "FORGE_AUTH_MODE",
            "FORGE_IDENTITY_URL",
            "FORGE_IDENTITY_CACHE_TTL_SECONDS",
            "FORGE_STORAGE_READY_RETRY_INITIAL_MS",
            "FORGE_STORAGE_READY_RETRY_MAX_MS",
            "FORGE_STORAGE_STREAM_BUFFER_BYTES",
            "FORGE_STORAGE_MAX_OBJECT_BYTES",
            "FORGE_STORAGE_VERIFY_ON_READ",
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
    fn defaults_port_and_root() {
        with_env(&[("PORT", None), ("FORGE_STORAGE_ROOT", None)], || {
            let cfg = Config::from_env().expect("config");
            assert_eq!(cfg.port, 4107);
            assert_eq!(cfg.service_name, "forge-storage");
            assert_eq!(cfg.storage_root, PathBuf::from("/data/storage"));
            assert_eq!(cfg.allowed_base, PathBuf::from("/data"));
            assert_eq!(cfg.meta_path, PathBuf::from("/data/storage/meta/index.db"));
            assert_eq!(cfg.auth_mode, AuthMode::Dev);
            assert_eq!(cfg.log_level, "info");
            assert_eq!(cfg.stream_buffer_bytes, DEFAULT_STREAM_BUFFER_BYTES);
            assert_eq!(cfg.max_object_bytes, None);
            assert_eq!(cfg.verify_on_read, VerifyOnRead::Off);
        });
    }

    #[test]
    fn rejects_missing_port() {
        with_env(&[("PORT", Some(""))], || {
            let err = Config::from_env().expect_err("empty PORT");
            assert!(err.contains("PORT"), "{err}");
        });
    }

    #[test]
    fn rejects_invalid_port() {
        with_env(&[("PORT", Some("not-a-port"))], || {
            let err = Config::from_env().expect_err("bad PORT");
            assert!(err.contains("PORT"), "{err}");
        });
    }

    #[test]
    fn rejects_zero_port() {
        with_env(&[("PORT", Some("0"))], || {
            let err = Config::from_env().expect_err("zero PORT");
            assert!(err.contains("PORT"), "{err}");
        });
    }

    #[test]
    fn parses_custom_root_and_base() {
        with_env(
            &[
                ("PORT", Some("4107")),
                ("FORGE_STORAGE_ROOT", Some("/var/lib/forge-storage")),
                ("FORGE_STORAGE_ALLOWED_BASE", Some("/var/lib")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.storage_root, PathBuf::from("/var/lib/forge-storage"));
                assert_eq!(cfg.allowed_base, PathBuf::from("/var/lib"));
                assert_eq!(
                    cfg.meta_path,
                    PathBuf::from("/var/lib/forge-storage/meta/index.db")
                );
            },
        );
    }

    #[test]
    fn enforce_requires_identity_url() {
        with_env(&[("FORGE_AUTH_MODE", Some("enforce"))], || {
            let err = Config::from_env().expect_err("missing identity");
            assert!(err.contains("FORGE_IDENTITY_URL"), "{err}");
        });
    }

    #[test]
    fn accepts_enforced_alias() {
        with_env(
            &[
                ("FORGE_AUTH_MODE", Some("enforced")),
                ("FORGE_IDENTITY_URL", Some("http://identity:8080")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.auth_mode, AuthMode::Enforce);
            },
        );
    }
}
