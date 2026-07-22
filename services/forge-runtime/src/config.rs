use std::env;
use std::path::PathBuf;
use std::time::Duration;

/// Env-backed configuration for forge-runtime.
#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
    pub auth_mode: String,
    pub docker_host: String,
    pub shutdown_grace: Duration,
    /// Bounded startup retries against Docker before continuing with readiness=503.
    pub docker_startup_retries: u32,
    pub docker_startup_retry_delay: Duration,
    /// Directory for persisted node identity (`node_id` file).
    pub data_dir: PathBuf,
    pub heartbeat_interval: Duration,
    /// Optional Control base URL for the outbound registration stub (04.07).
    pub control_url: Option<String>,
    /// Max time to wait for an image pull.
    pub pull_timeout: Duration,
    /// Informational default registry host (images are fully qualified).
    pub default_registry: String,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let port_raw = env::var("PORT").unwrap_or_default();
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

        let service_name = non_empty_env("FORGE_SERVICE_NAME", "forge-runtime");
        let service_version = non_empty_env("FORGE_SERVICE_VERSION", "0.1.0");
        let env_name = non_empty_env("FORGE_ENV", "development");
        let auth_mode = non_empty_env("FORGE_AUTH_MODE", "dev");

        let docker_host = env::var("DOCKER_HOST")
            .unwrap_or_else(|_| "unix:///var/run/docker.sock".into())
            .trim()
            .to_string();
        let docker_host = if docker_host.is_empty() {
            "unix:///var/run/docker.sock".into()
        } else {
            docker_host
        };

        let grace_raw = env::var("FORGE_SHUTDOWN_GRACE_SECONDS").unwrap_or_else(|_| "10".into());
        let grace_secs: u64 = grace_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got {grace_raw:?}"
            )
        })?;

        let retries_raw = env::var("FORGE_DOCKER_STARTUP_RETRIES").unwrap_or_else(|_| "5".into());
        let docker_startup_retries: u32 = retries_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_DOCKER_STARTUP_RETRIES must be a non-negative integer, got {retries_raw:?}"
            )
        })?;

        let delay_raw =
            env::var("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS").unwrap_or_else(|_| "500".into());
        let delay_ms: u64 = delay_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_DOCKER_STARTUP_RETRY_DELAY_MS must be a non-negative integer, got {delay_raw:?}"
            )
        })?;

        let data_dir_raw =
            env::var("FORGE_RUNTIME_DATA_DIR").unwrap_or_else(|_| "/var/lib/forge-runtime".into());
        let data_dir_raw = data_dir_raw.trim();
        if data_dir_raw.is_empty() {
            return Err("FORGE_RUNTIME_DATA_DIR must not be empty".into());
        }
        let data_dir = PathBuf::from(data_dir_raw);

        let hb_raw = env::var("FORGE_HEARTBEAT_INTERVAL_SECONDS").unwrap_or_else(|_| "10".into());
        let hb_secs: u64 = hb_raw.trim().parse().map_err(|_| {
            format!("FORGE_HEARTBEAT_INTERVAL_SECONDS must be a positive integer, got {hb_raw:?}")
        })?;
        if hb_secs == 0 {
            return Err(format!(
                "FORGE_HEARTBEAT_INTERVAL_SECONDS must be a positive integer, got {hb_raw:?}"
            ));
        }

        let control_url = env::var("FORGE_CONTROL_URL")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let pull_raw = env::var("FORGE_PULL_TIMEOUT_SECONDS").unwrap_or_else(|_| "120".into());
        let pull_secs: u64 = pull_raw.trim().parse().map_err(|_| {
            format!("FORGE_PULL_TIMEOUT_SECONDS must be a positive integer, got {pull_raw:?}")
        })?;
        if pull_secs == 0 {
            return Err(format!(
                "FORGE_PULL_TIMEOUT_SECONDS must be a positive integer, got {pull_raw:?}"
            ));
        }

        let default_registry = non_empty_env("FORGE_DEFAULT_REGISTRY", "localhost:5000");

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
            auth_mode,
            docker_host,
            shutdown_grace: Duration::from_secs(grace_secs),
            docker_startup_retries,
            docker_startup_retry_delay: Duration::from_millis(delay_ms),
            data_dir,
            heartbeat_interval: Duration::from_secs(hb_secs),
            control_url,
            pull_timeout: Duration::from_secs(pull_secs),
            default_registry,
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
            "FORGE_AUTH_MODE",
            "DOCKER_HOST",
            "FORGE_SHUTDOWN_GRACE_SECONDS",
            "FORGE_DOCKER_STARTUP_RETRIES",
            "FORGE_DOCKER_STARTUP_RETRY_DELAY_MS",
            "FORGE_RUNTIME_DATA_DIR",
            "FORGE_HEARTBEAT_INTERVAL_SECONDS",
            "FORGE_CONTROL_URL",
            "FORGE_PULL_TIMEOUT_SECONDS",
            "FORGE_DEFAULT_REGISTRY",
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
    fn requires_port() {
        with_env(&[("PORT", None), ("FORGE_LOG_LEVEL", Some("info"))], || {
            assert!(Config::from_env().is_err());
        });
    }

    #[test]
    fn rejects_invalid_port() {
        with_env(
            &[
                ("PORT", Some("not-a-port")),
                ("FORGE_LOG_LEVEL", Some("info")),
            ],
            || {
                assert!(Config::from_env().is_err());
            },
        );
    }

    #[test]
    fn rejects_zero_port() {
        with_env(
            &[("PORT", Some("0")), ("FORGE_LOG_LEVEL", Some("info"))],
            || {
                assert!(Config::from_env().is_err());
            },
        );
    }

    #[test]
    fn rejects_invalid_log_level() {
        with_env(
            &[("PORT", Some("8080")), ("FORGE_LOG_LEVEL", Some("verbose"))],
            || {
                assert!(Config::from_env().is_err());
            },
        );
    }

    #[test]
    fn loads_defaults() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_LOG_LEVEL", Some("info")),
                ("FORGE_SERVICE_NAME", None),
                ("FORGE_SERVICE_VERSION", None),
                ("FORGE_ENV", None),
                ("FORGE_AUTH_MODE", None),
                ("DOCKER_HOST", None),
                ("FORGE_SHUTDOWN_GRACE_SECONDS", None),
                ("FORGE_RUNTIME_DATA_DIR", None),
                ("FORGE_HEARTBEAT_INTERVAL_SECONDS", None),
                ("FORGE_CONTROL_URL", None),
                ("FORGE_PULL_TIMEOUT_SECONDS", None),
                ("FORGE_DEFAULT_REGISTRY", None),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.port, 8080);
                assert_eq!(cfg.service_name, "forge-runtime");
                assert_eq!(cfg.service_version, "0.1.0");
                assert_eq!(cfg.log_level, "info");
                assert_eq!(cfg.env, "development");
                assert_eq!(cfg.auth_mode, "dev");
                assert_eq!(cfg.docker_host, "unix:///var/run/docker.sock");
                assert_eq!(cfg.shutdown_grace, Duration::from_secs(10));
                assert_eq!(cfg.data_dir, PathBuf::from("/var/lib/forge-runtime"));
                assert_eq!(cfg.heartbeat_interval, Duration::from_secs(10));
                assert!(cfg.control_url.is_none());
                assert_eq!(cfg.pull_timeout, Duration::from_secs(120));
                assert_eq!(cfg.default_registry, "localhost:5000");
            },
        );
    }

    #[test]
    fn rejects_zero_heartbeat_interval() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_HEARTBEAT_INTERVAL_SECONDS", Some("0")),
            ],
            || {
                assert!(Config::from_env().is_err());
            },
        );
    }

    #[test]
    fn loads_control_url() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_CONTROL_URL", Some("http://forge-control:8080")),
                ("FORGE_RUNTIME_DATA_DIR", Some("/tmp/forge-runtime-data")),
                ("FORGE_HEARTBEAT_INTERVAL_SECONDS", Some("5")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(
                    cfg.control_url.as_deref(),
                    Some("http://forge-control:8080")
                );
                assert_eq!(cfg.data_dir, PathBuf::from("/tmp/forge-runtime-data"));
                assert_eq!(cfg.heartbeat_interval, Duration::from_secs(5));
            },
        );
    }

    #[test]
    fn loads_docker_host_override() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("DOCKER_HOST", Some("unix:///tmp/bad.sock")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.docker_host, "unix:///tmp/bad.sock");
            },
        );
    }
}
