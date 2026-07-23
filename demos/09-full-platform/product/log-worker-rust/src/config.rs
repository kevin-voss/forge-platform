use std::env;

#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
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

        let service_name = env::var("FORGE_SERVICE_NAME")
            .unwrap_or_else(|_| "incident-log-worker".into())
            .trim()
            .to_string();
        let service_name = if service_name.is_empty() {
            "incident-log-worker".into()
        } else {
            service_name
        };

        let service_version = env::var("FORGE_SERVICE_VERSION")
            .unwrap_or_else(|_| "0.1.0".into())
            .trim()
            .to_string();
        let service_version = if service_version.is_empty() {
            "0.1.0".into()
        } else {
            service_version
        };

        let env_name = env::var("FORGE_ENV")
            .unwrap_or_else(|_| "development".into())
            .trim()
            .to_string();
        let env_name = if env_name.is_empty() {
            "development".into()
        } else {
            env_name
        };

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
        })
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
        ];
        let previous: Vec<(String, Option<String>)> = keys
            .iter()
            .map(|k| ((*k).to_string(), env::var(k).ok()))
            .collect();

        for k in keys {
            env::remove_var(k);
        }
        for (k, v) in vars {
            match v {
                Some(val) => env::set_var(k, val),
                None => env::remove_var(k),
            }
        }

        f();

        for (k, v) in previous {
            match v {
                Some(val) => env::set_var(&k, val),
                None => env::remove_var(&k),
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
    fn loads_defaults() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_LOG_LEVEL", Some("info")),
                ("FORGE_SERVICE_NAME", None),
                ("FORGE_SERVICE_VERSION", None),
                ("FORGE_ENV", None),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.port, 8080);
                assert_eq!(cfg.service_name, "incident-log-worker");
                assert_eq!(cfg.service_version, "0.1.0");
            },
        );
    }
}
