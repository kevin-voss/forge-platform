use async_trait::async_trait;
use bollard::Docker;
use std::sync::Arc;
use std::time::Duration;
use tracing::{info, warn};

/// Probe used by readiness. Production uses bollard; tests inject stubs.
#[async_trait]
pub trait DockerProbe: Send + Sync {
    async fn ping(&self) -> Result<(), String>;
    async fn engine_version(&self) -> Result<String, String>;
}

/// Docker Engine client. Connection failures are deferred to `ping` so the HTTP
/// server can still expose liveness while readiness returns 503.
#[derive(Clone)]
pub struct BollardDocker {
    inner: Result<Arc<Docker>, String>,
}

impl BollardDocker {
    pub fn connect(docker_host: &str) -> Self {
        match connect_docker(docker_host) {
            Ok(docker) => Self {
                inner: Ok(Arc::new(docker)),
            },
            Err(err) => {
                warn!(error = %err, docker_host = %docker_host, "docker client connect failed");
                Self { inner: Err(err) }
            }
        }
    }
}

#[async_trait]
impl DockerProbe for BollardDocker {
    async fn ping(&self) -> Result<(), String> {
        let docker = self.inner.as_ref().map_err(|e| e.clone())?;
        docker
            .ping()
            .await
            .map(|_| ())
            .map_err(|e| format!("docker ping failed: {e}"))
    }

    async fn engine_version(&self) -> Result<String, String> {
        let docker = self.inner.as_ref().map_err(|e| e.clone())?;
        let version = docker
            .version()
            .await
            .map_err(|e| format!("docker version failed: {e}"))?;
        Ok(version.version.unwrap_or_else(|| "unknown".into()))
    }
}

fn connect_docker(docker_host: &str) -> Result<Docker, String> {
    let host = docker_host.trim();
    if host.is_empty() || host == "unix:///var/run/docker.sock" {
        return Docker::connect_with_local_defaults()
            .map_err(|e| format!("docker connect (local defaults): {e}"));
    }

    if let Some(path) = host.strip_prefix("unix://") {
        return Docker::connect_with_unix(path, 120, bollard::API_DEFAULT_VERSION)
            .map_err(|e| format!("docker connect (unix {path}): {e}"));
    }

    if host.starts_with("tcp://") || host.starts_with("http://") || host.starts_with("https://") {
        return Docker::connect_with_http(host, 120, bollard::API_DEFAULT_VERSION)
            .map_err(|e| format!("docker connect (http {host}): {e}"));
    }

    // Treat bare paths as unix sockets.
    Docker::connect_with_unix(host, 120, bollard::API_DEFAULT_VERSION)
        .map_err(|e| format!("docker connect (unix {host}): {e}"))
}

/// Bounded startup ping. On exhaustion, logs a warning and returns the last error
/// without exiting — readiness will remain 503 until Docker becomes reachable.
pub async fn startup_ping(
    probe: &dyn DockerProbe,
    retries: u32,
    delay: Duration,
) -> Result<String, String> {
    let attempts = retries.saturating_add(1);
    let mut last_err = String::from("docker unavailable");

    for attempt in 1..=attempts {
        match probe.ping().await {
            Ok(()) => match probe.engine_version().await {
                Ok(version) => {
                    info!(
                        docker_engine_version = %version,
                        attempt,
                        "docker engine reachable"
                    );
                    return Ok(version);
                }
                Err(err) => {
                    // Ping worked; still surface version when possible.
                    warn!(error = %err, "docker ping ok but version unavailable");
                    return Ok("unknown".into());
                }
            },
            Err(err) => {
                last_err = err;
                warn!(
                    error = %last_err,
                    attempt,
                    max_attempts = attempts,
                    "docker ping failed during startup"
                );
                if attempt < attempts {
                    tokio::time::sleep(delay).await;
                }
            }
        }
    }

    Err(last_err)
}

#[cfg(test)]
pub mod test_support {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    pub struct StubDocker {
        pub ping_ok: bool,
        pub version: String,
        pub ping_calls: AtomicUsize,
    }

    impl StubDocker {
        pub fn ok(version: impl Into<String>) -> Self {
            Self {
                ping_ok: true,
                version: version.into(),
                ping_calls: AtomicUsize::new(0),
            }
        }

        pub fn down() -> Self {
            Self {
                ping_ok: false,
                version: String::new(),
                ping_calls: AtomicUsize::new(0),
            }
        }
    }

    #[async_trait]
    impl DockerProbe for StubDocker {
        async fn ping(&self) -> Result<(), String> {
            self.ping_calls.fetch_add(1, Ordering::SeqCst);
            if self.ping_ok {
                Ok(())
            } else {
                Err("stub: docker unreachable".into())
            }
        }

        async fn engine_version(&self) -> Result<String, String> {
            if self.ping_ok {
                Ok(self.version.clone())
            } else {
                Err("stub: docker unreachable".into())
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::test_support::StubDocker;
    use super::*;
    use std::sync::atomic::Ordering;

    #[tokio::test]
    async fn startup_ping_succeeds() {
        let stub = StubDocker::ok("29.1.3");
        let version = startup_ping(&stub, 0, Duration::from_millis(1))
            .await
            .expect("ping");
        assert_eq!(version, "29.1.3");
        assert_eq!(stub.ping_calls.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn startup_ping_retries_then_fails() {
        let stub = StubDocker::down();
        let err = startup_ping(&stub, 2, Duration::from_millis(1))
            .await
            .expect_err("should fail");
        assert!(err.contains("unreachable"));
        assert_eq!(stub.ping_calls.load(Ordering::SeqCst), 3);
    }

    #[tokio::test]
    async fn missing_socket_defers_failure_to_ping() {
        let docker = BollardDocker::connect("unix:///tmp/forge-runtime-missing.sock");
        let err = docker.ping().await.expect_err("ping should fail");
        assert!(!err.is_empty());
    }
}
