use std::env;

#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub service_name: String,
    pub service_version: String,
    pub log_level: String,
    pub env: String,
    pub events_url: String,
    pub events_consumer: String,
    pub events_subject: String,
    pub events_poll_ms: u64,
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

        let mut events_url = env::var("FORGE_EVENTS_URL")
            .unwrap_or_default()
            .trim()
            .to_string();
        if events_url.is_empty() {
            // Reach host-published Events (Runtime workloads + Docker Desktop).
            events_url = "http://host.docker.internal:4105".into();
        }

        let events_consumer = env::var("FORGE_EVENTS_CONSUMER")
            .unwrap_or_else(|_| "incident-log-worker".into())
            .trim()
            .to_string();
        let events_consumer = if events_consumer.is_empty() {
            "incident-log-worker".into()
        } else {
            events_consumer
        };

        let events_subject = env::var("FORGE_EVENTS_SUBJECT")
            .unwrap_or_else(|_| "incident.created".into())
            .trim()
            .to_string();
        let events_subject = if events_subject.is_empty() {
            "incident.created".into()
        } else {
            events_subject
        };

        let poll_raw = env::var("FORGE_EVENTS_POLL_MS").unwrap_or_else(|_| "500".into());
        let events_poll_ms: u64 = poll_raw
            .trim()
            .parse()
            .map_err(|_| format!("FORGE_EVENTS_POLL_MS must be an integer, got {poll_raw:?}"))?;

        Ok(Self {
            port,
            service_name,
            service_version,
            log_level,
            env: env_name,
            events_url,
            events_consumer,
            events_subject,
            events_poll_ms,
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
            "FORGE_EVENTS_URL",
            "FORGE_DEPLOYMENT_ID",
            "FORGE_EVENTS_CONSUMER",
            "FORGE_EVENTS_SUBJECT",
            "FORGE_EVENTS_POLL_MS",
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
                ("FORGE_EVENTS_URL", None),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.port, 8080);
                assert_eq!(cfg.service_name, "incident-log-worker");
                assert_eq!(cfg.service_version, "0.1.0");
                assert_eq!(cfg.events_url, "http://host.docker.internal:4105");
            },
        );
    }
}
