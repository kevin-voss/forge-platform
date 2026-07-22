use std::env;
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
