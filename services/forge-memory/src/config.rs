use std::env;
use std::path::{Path, PathBuf};
use std::time::Duration;

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
            "FORGE_MEMORY_ROOT",
            "FORGE_MEMORY_ALLOWED_BASE",
            "FORGE_MEMORY_READY_RETRY_INITIAL_MS",
            "FORGE_MEMORY_READY_RETRY_MAX_MS",
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
