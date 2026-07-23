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

impl fmt::Display for AuthMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Dev => write!(f, "dev"),
            Self::Enforce => write!(f, "enforce"),
        }
    }
}

/// Env-backed configuration for forge-memory.
#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
    pub shutdown_grace: Duration,
    pub memory_root: PathBuf,
    pub allowed_base: PathBuf,
    pub ready_retry_initial: Duration,
    pub ready_retry_max: Duration,
    pub max_dim: usize,
    pub list_page_size: usize,
    pub max_metadata_bytes: usize,
    pub max_top_k: usize,
    pub max_upsert_batch: usize,
    pub compact_on_boot: bool,
    pub auth_mode: AuthMode,
    pub identity_url: Option<String>,
    pub identity_cache_ttl_secs: u64,
    pub models_url: String,
    pub default_embed_model: String,
    pub models_timeout: Duration,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let port_raw = env::var("PORT").unwrap_or_else(|_| "4303".into());
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

        let service_name = non_empty_env("FORGE_SERVICE_NAME", "forge-memory");
        let service_version = non_empty_env("FORGE_SERVICE_VERSION", "0.1.0");
        let env_name = non_empty_env("FORGE_ENV", "development");

        let grace_raw = env::var("FORGE_SHUTDOWN_GRACE_SECONDS").unwrap_or_else(|_| "10".into());
        let grace_secs: u64 = grace_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got {grace_raw:?}"
            )
        })?;

        let memory_root = PathBuf::from(
            env::var("FORGE_MEMORY_ROOT")
                .unwrap_or_else(|_| "/data/memory".into())
                .trim(),
        );
        if memory_root.as_os_str().is_empty() {
            return Err("FORGE_MEMORY_ROOT must not be empty".into());
        }

        let allowed_base = match env::var("FORGE_MEMORY_ALLOWED_BASE") {
            Ok(v) if !v.trim().is_empty() => PathBuf::from(v.trim()),
            _ => memory_root
                .parent()
                .filter(|p| !p.as_os_str().is_empty())
                .map(Path::to_path_buf)
                .unwrap_or_else(|| PathBuf::from("/")),
        };

        let ready_retry_initial =
            Duration::from_millis(parse_u64_env("FORGE_MEMORY_READY_RETRY_INITIAL_MS", 500)?);
        let ready_retry_max =
            Duration::from_millis(parse_u64_env("FORGE_MEMORY_READY_RETRY_MAX_MS", 10_000)?);

        let max_dim = parse_u64_env("FORGE_MEMORY_MAX_DIM", 4096)? as usize;
        if max_dim == 0 {
            return Err("FORGE_MEMORY_MAX_DIM must be >= 1".into());
        }
        let list_page_size = parse_u64_env("FORGE_MEMORY_LIST_PAGE_SIZE", 100)? as usize;
        if list_page_size == 0 {
            return Err("FORGE_MEMORY_LIST_PAGE_SIZE must be >= 1".into());
        }
        let max_metadata_bytes = parse_u64_env("FORGE_MEMORY_MAX_METADATA_BYTES", 65_536)? as usize;
        if max_metadata_bytes == 0 {
            return Err("FORGE_MEMORY_MAX_METADATA_BYTES must be >= 1".into());
        }
        let max_top_k = parse_u64_env("FORGE_MEMORY_MAX_TOP_K", 100)? as usize;
        if max_top_k == 0 {
            return Err("FORGE_MEMORY_MAX_TOP_K must be >= 1".into());
        }
        let max_upsert_batch = parse_u64_env("FORGE_MEMORY_MAX_UPSERT_BATCH", 512)? as usize;
        if max_upsert_batch == 0 {
            return Err("FORGE_MEMORY_MAX_UPSERT_BATCH must be >= 1".into());
        }
        let compact_on_boot = parse_bool_env("FORGE_MEMORY_COMPACT_ON_BOOT", true)?;

        let auth_mode =
            AuthMode::parse(&env::var("FORGE_AUTH_MODE").unwrap_or_else(|_| "dev".into()))?;
        let identity_url = match env::var("FORGE_IDENTITY_URL") {
            Ok(v) if !v.trim().is_empty() => Some(v.trim().to_string()),
            _ => None,
        };
        let identity_cache_ttl_secs = parse_u64_env("FORGE_INTROSPECT_CACHE_TTL_S", 10)?;
        if auth_mode == AuthMode::Enforce && identity_url.is_none() {
            return Err("FORGE_IDENTITY_URL is required when FORGE_AUTH_MODE=enforce".into());
        }

        let models_url = non_empty_env("FORGE_MODELS_URL", "http://forge-models:4300");
        if !(models_url.starts_with("http://") || models_url.starts_with("https://")) {
            return Err(format!(
                "FORGE_MODELS_URL must be an absolute http(s) URL, got {models_url:?}"
            ));
        }
        let default_embed_model = non_empty_env("FORGE_MEMORY_DEFAULT_MODEL", "local-embed-small");
        let models_timeout =
            Duration::from_secs(parse_u64_env("FORGE_MEMORY_MODELS_TIMEOUT_SECONDS", 15)?.max(1));

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
            shutdown_grace: Duration::from_secs(grace_secs),
            memory_root,
            allowed_base,
            ready_retry_initial,
            ready_retry_max,
            max_dim,
            list_page_size,
            max_metadata_bytes,
            max_top_k,
            max_upsert_batch,
            compact_on_boot,
            auth_mode,
            identity_url,
            identity_cache_ttl_secs,
            models_url,
            default_embed_model,
            models_timeout,
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

fn parse_bool_env(key: &str, default: bool) -> Result<bool, String> {
    match env::var(key) {
        Ok(raw) => {
            let trimmed = raw.trim().to_ascii_lowercase();
            if trimmed.is_empty() {
                return Ok(default);
            }
            match trimmed.as_str() {
                "1" | "true" | "yes" | "on" => Ok(true),
                "0" | "false" | "no" | "off" => Ok(false),
                other => Err(format!("{key} must be true|false (or 1|0), got {other:?}")),
            }
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
            "FORGE_MEMORY_ROOT",
            "FORGE_MEMORY_ALLOWED_BASE",
            "FORGE_MEMORY_READY_RETRY_INITIAL_MS",
            "FORGE_MEMORY_READY_RETRY_MAX_MS",
            "FORGE_MEMORY_MAX_DIM",
            "FORGE_MEMORY_LIST_PAGE_SIZE",
            "FORGE_MEMORY_MAX_METADATA_BYTES",
            "FORGE_MEMORY_MAX_TOP_K",
            "FORGE_MEMORY_MAX_UPSERT_BATCH",
            "FORGE_MEMORY_COMPACT_ON_BOOT",
            "FORGE_AUTH_MODE",
            "FORGE_IDENTITY_URL",
            "FORGE_INTROSPECT_CACHE_TTL_S",
            "FORGE_MODELS_URL",
            "FORGE_MEMORY_DEFAULT_MODEL",
            "FORGE_MEMORY_MODELS_TIMEOUT_SECONDS",
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
        with_env(&[("PORT", None), ("FORGE_MEMORY_ROOT", None)], || {
            let cfg = Config::from_env().expect("config");
            assert_eq!(cfg.port, 4303);
            assert_eq!(cfg.service_name, "forge-memory");
            assert_eq!(cfg.memory_root, PathBuf::from("/data/memory"));
            assert_eq!(cfg.allowed_base, PathBuf::from("/data"));
            assert_eq!(cfg.log_level, "info");
            assert_eq!(cfg.auth_mode, AuthMode::Dev);
            assert_eq!(cfg.models_url, "http://forge-models:4300");
            assert_eq!(cfg.default_embed_model, "local-embed-small");
        });
    }

    #[test]
    fn rejects_invalid_models_url() {
        with_env(&[("FORGE_MODELS_URL", Some("not-a-url"))], || {
            let err = Config::from_env().expect_err("bad models url");
            assert!(err.contains("FORGE_MODELS_URL"), "{err}");
        });
    }

    #[test]
    fn enforce_requires_identity_url() {
        with_env(&[("FORGE_AUTH_MODE", Some("enforce"))], || {
            let err = Config::from_env().expect_err("enforce without identity");
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
                ("PORT", Some("4303")),
                ("FORGE_MEMORY_ROOT", Some("/var/lib/forge-memory")),
                ("FORGE_MEMORY_ALLOWED_BASE", Some("/var/lib")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.memory_root, PathBuf::from("/var/lib/forge-memory"));
                assert_eq!(cfg.allowed_base, PathBuf::from("/var/lib"));
            },
        );
    }
}
