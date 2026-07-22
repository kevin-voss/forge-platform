use async_trait::async_trait;
use bollard::container::{
    Config, CreateContainerOptions, InspectContainerOptions, ListContainersOptions,
    RemoveContainerOptions, StartContainerOptions,
};
use bollard::image::CreateImageOptions;
use bollard::models::{HostConfig, PortBinding};
use bollard::Docker;

/// Label filter for managed workloads (kept here to avoid a docker↔workload cycle).
const MANAGED_LABEL_FILTER: &str = "forge.managed=true";
const DEPLOYMENT_ID_LABEL: &str = "forge.deployment_id";
use futures_util::TryStreamExt;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tracing::{info, warn};

/// Probe used by readiness. Production uses bollard; tests inject stubs.
#[async_trait]
pub trait DockerProbe: Send + Sync {
    async fn ping(&self) -> Result<(), String>;
    async fn engine_version(&self) -> Result<String, String>;
}

/// Parameters for creating a managed workload container.
#[derive(Debug, Clone)]
pub struct CreateWorkloadParams {
    pub name: String,
    pub image: String,
    pub container_port: u16,
    pub env: HashMap<String, String>,
    pub labels: HashMap<String, String>,
}

/// Subset of inspect facts Runtime needs for workload APIs.
#[derive(Debug, Clone)]
pub struct ContainerInspectInfo {
    pub id: String,
    pub image: Option<String>,
    pub state: String,
    /// Map of `"8080/tcp"` → published host ports.
    pub port_bindings: HashMap<String, Vec<u16>>,
    pub labels: Option<HashMap<String, String>>,
    /// First non-empty container IP (bridge / network), when available.
    pub ip_address: Option<String>,
    pub restart_count: u32,
}

/// Docker Engine operations for readiness + workload lifecycle.
#[async_trait]
pub trait DockerEngine: DockerProbe {
    async fn pull_image(&self, image: &str, timeout: Duration) -> Result<(), String>;
    async fn create_container(&self, params: &CreateWorkloadParams) -> Result<String, String>;
    async fn start_container(&self, id_or_name: &str) -> Result<(), String>;
    async fn remove_container(&self, id_or_name: &str, force: bool) -> Result<(), String>;
    async fn inspect_container(&self, id_or_name: &str) -> Result<ContainerInspectInfo, String>;
    /// List containers labeled `forge.managed=true` (all states).
    async fn list_managed_containers(&self) -> Result<Vec<ContainerInspectInfo>, String>;
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

    fn client(&self) -> Result<Arc<Docker>, String> {
        self.inner.as_ref().map(Arc::clone).map_err(|e| e.clone())
    }
}

#[async_trait]
impl DockerProbe for BollardDocker {
    async fn ping(&self) -> Result<(), String> {
        let docker = self.client()?;
        docker
            .ping()
            .await
            .map(|_| ())
            .map_err(|e| format!("docker ping failed: {e}"))
    }

    async fn engine_version(&self) -> Result<String, String> {
        let docker = self.client()?;
        let version = docker
            .version()
            .await
            .map_err(|e| format!("docker version failed: {e}"))?;
        Ok(version.version.unwrap_or_else(|| "unknown".into()))
    }
}

#[async_trait]
impl DockerEngine for BollardDocker {
    async fn pull_image(&self, image: &str, timeout: Duration) -> Result<(), String> {
        let docker = self.client()?;
        let options = CreateImageOptions {
            from_image: image,
            ..Default::default()
        };

        let pull = async {
            let mut stream = docker.create_image(Some(options), None, None);
            while let Some(item) = stream
                .try_next()
                .await
                .map_err(|e| format!("image pull failed: {e}"))?
            {
                if let Some(err) = item.error {
                    return Err(format!("image pull failed: {err}"));
                }
            }
            Ok(())
        };

        match tokio::time::timeout(timeout, pull).await {
            Ok(result) => result,
            Err(_) => Err(format!(
                "image pull timed out after {}s for {image}",
                timeout.as_secs()
            )),
        }
    }

    async fn create_container(&self, params: &CreateWorkloadParams) -> Result<String, String> {
        let docker = self.client()?;
        let port_key = format!("{}/tcp", params.container_port);

        let mut exposed: HashMap<String, HashMap<(), ()>> = HashMap::new();
        exposed.insert(port_key.clone(), HashMap::new());

        let mut port_bindings: HashMap<String, Option<Vec<PortBinding>>> = HashMap::new();
        // Empty host_port → Docker assigns an ephemeral host port.
        port_bindings.insert(
            port_key,
            Some(vec![PortBinding {
                host_ip: Some("0.0.0.0".into()),
                host_port: None,
            }]),
        );

        let env: Vec<String> = params.env.iter().map(|(k, v)| format!("{k}={v}")).collect();

        let config = Config {
            image: Some(params.image.clone()),
            env: Some(env),
            labels: Some(params.labels.clone()),
            exposed_ports: Some(exposed),
            host_config: Some(HostConfig {
                port_bindings: Some(port_bindings),
                // Publish all exposed ports (bindings already set explicitly).
                publish_all_ports: Some(false),
                ..Default::default()
            }),
            ..Default::default()
        };

        let response = docker
            .create_container(
                Some(CreateContainerOptions {
                    name: params.name.as_str(),
                    platform: None,
                }),
                config,
            )
            .await
            .map_err(|e| format!("container create failed: {e}"))?;

        Ok(response.id)
    }

    async fn start_container(&self, id_or_name: &str) -> Result<(), String> {
        let docker = self.client()?;
        docker
            .start_container(id_or_name, None::<StartContainerOptions<String>>)
            .await
            .map_err(|e| format!("container start failed: {e}"))
    }

    async fn remove_container(&self, id_or_name: &str, force: bool) -> Result<(), String> {
        let docker = self.client()?;
        docker
            .remove_container(
                id_or_name,
                Some(RemoveContainerOptions {
                    force,
                    v: true,
                    link: false,
                }),
            )
            .await
            .map_err(|e| format!("container remove failed: {e}"))
    }

    async fn inspect_container(&self, id_or_name: &str) -> Result<ContainerInspectInfo, String> {
        let docker = self.client()?;
        let inspect = docker
            .inspect_container(id_or_name, None::<InspectContainerOptions>)
            .await
            .map_err(|e| format!("container inspect failed: {e}"))?;
        Ok(inspect_to_info(inspect, id_or_name))
    }

    async fn list_managed_containers(&self) -> Result<Vec<ContainerInspectInfo>, String> {
        let docker = self.client()?;
        let mut filters = HashMap::new();
        filters.insert("label".to_string(), vec![MANAGED_LABEL_FILTER.to_string()]);
        let options = ListContainersOptions {
            all: true,
            filters,
            ..Default::default()
        };
        let listed = docker
            .list_containers(Some(options))
            .await
            .map_err(|e| format!("list containers failed: {e}"))?;

        let mut out = Vec::with_capacity(listed.len());
        for summary in listed {
            let id = summary.id.unwrap_or_default();
            if id.is_empty() {
                continue;
            }
            // Prefer full inspect so port bindings / IP are accurate.
            match self.inspect_container(&id).await {
                Ok(info) => {
                    // Require deployment id label for probing targets.
                    if info
                        .labels
                        .as_ref()
                        .and_then(|l| l.get(DEPLOYMENT_ID_LABEL))
                        .map(|s| !s.is_empty())
                        .unwrap_or(false)
                    {
                        out.push(info);
                    }
                }
                Err(err) => {
                    warn!(container_id = %id, error = %err, "skip managed container inspect");
                }
            }
        }
        Ok(out)
    }
}

fn inspect_to_info(
    inspect: bollard::models::ContainerInspectResponse,
    fallback_id: &str,
) -> ContainerInspectInfo {
    let id = inspect
        .id
        .clone()
        .unwrap_or_else(|| fallback_id.to_string());
    let image = inspect.config.as_ref().and_then(|c| c.image.clone());
    let state = inspect
        .state
        .as_ref()
        .and_then(|s| s.status.map(|st| st.to_string()))
        .unwrap_or_default();
    let labels = inspect.config.as_ref().and_then(|c| c.labels.clone());
    let restart_count = inspect.restart_count.unwrap_or(0).clamp(0, u32::MAX as i64) as u32;

    let mut port_bindings: HashMap<String, Vec<u16>> = HashMap::new();
    if let Some(ports) = inspect
        .network_settings
        .as_ref()
        .and_then(|n| n.ports.as_ref())
    {
        for (key, bindings) in ports {
            let mut hosts = Vec::new();
            if let Some(list) = bindings {
                for b in list {
                    if let Some(hp) = &b.host_port {
                        if let Ok(p) = hp.parse::<u16>() {
                            hosts.push(p);
                        }
                    }
                }
            }
            if !hosts.is_empty() {
                port_bindings.insert(key.clone(), hosts);
            }
        }
    }

    let mut ip_address = None;
    if let Some(networks) = inspect
        .network_settings
        .as_ref()
        .and_then(|n| n.networks.as_ref())
    {
        for net in networks.values() {
            if let Some(ip) = net.ip_address.as_ref() {
                if !ip.is_empty() {
                    ip_address = Some(ip.clone());
                    break;
                }
            }
        }
    }
    if ip_address.is_none() {
        if let Some(ip) = inspect
            .network_settings
            .as_ref()
            .and_then(|n| n.ip_address.clone())
        {
            if !ip.is_empty() {
                ip_address = Some(ip);
            }
        }
    }

    ContainerInspectInfo {
        id,
        image,
        state,
        port_bindings,
        labels,
        ip_address,
        restart_count,
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
    use std::sync::Mutex;

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

    #[async_trait]
    impl DockerEngine for StubDocker {
        async fn pull_image(&self, _image: &str, _timeout: Duration) -> Result<(), String> {
            Err("stub: pull not implemented".into())
        }

        async fn create_container(&self, _params: &CreateWorkloadParams) -> Result<String, String> {
            Err("stub: create not implemented".into())
        }

        async fn start_container(&self, _id_or_name: &str) -> Result<(), String> {
            Err("stub: start not implemented".into())
        }

        async fn remove_container(&self, _id_or_name: &str, _force: bool) -> Result<(), String> {
            Ok(())
        }

        async fn inspect_container(
            &self,
            _id_or_name: &str,
        ) -> Result<ContainerInspectInfo, String> {
            Err("stub: inspect not implemented".into())
        }

        async fn list_managed_containers(&self) -> Result<Vec<ContainerInspectInfo>, String> {
            Ok(Vec::new())
        }
    }

    /// Records Docker call order for workload orchestration unit tests.
    pub struct RecordingDocker {
        pub fail_op: Option<&'static str>,
        pub host_port: u16,
        pub calls: Mutex<Vec<&'static str>>,
        pub created_name: Mutex<Option<String>>,
        pub created_labels: Mutex<HashMap<String, String>>,
        pub created_image: Mutex<Option<String>>,
        pub created_port: Mutex<Option<u16>>,
        pub container_id: String,
        pub remove_calls: AtomicUsize,
        pub missing: bool,
        pub started: Mutex<bool>,
    }

    impl RecordingDocker {
        pub fn ok(host_port: u16) -> Self {
            Self {
                fail_op: None,
                host_port,
                calls: Mutex::new(Vec::new()),
                created_name: Mutex::new(None),
                created_labels: Mutex::new(HashMap::new()),
                created_image: Mutex::new(None),
                created_port: Mutex::new(None),
                container_id: "container-deadbeef".into(),
                remove_calls: AtomicUsize::new(0),
                missing: false,
                started: Mutex::new(false),
            }
        }

        pub fn fail_on(op: &'static str) -> Self {
            let mut d = Self::ok(40000);
            d.fail_op = Some(op);
            d
        }

        pub fn missing() -> Self {
            let mut d = Self::ok(0);
            d.missing = true;
            d
        }

        fn record(&self, op: &'static str) {
            self.calls.lock().expect("calls").push(op);
        }

        fn maybe_fail(&self, op: &'static str) -> Result<(), String> {
            self.record(op);
            if self.fail_op == Some(op) {
                Err(format!("stub: {op} failed"))
            } else {
                Ok(())
            }
        }
    }

    #[async_trait]
    impl DockerProbe for RecordingDocker {
        async fn ping(&self) -> Result<(), String> {
            Ok(())
        }

        async fn engine_version(&self) -> Result<String, String> {
            Ok("test".into())
        }
    }

    #[async_trait]
    impl DockerEngine for RecordingDocker {
        async fn pull_image(&self, _image: &str, _timeout: Duration) -> Result<(), String> {
            self.maybe_fail("pull")
        }

        async fn create_container(&self, params: &CreateWorkloadParams) -> Result<String, String> {
            self.maybe_fail("create")?;
            *self.created_name.lock().unwrap() = Some(params.name.clone());
            *self.created_labels.lock().unwrap() = params.labels.clone();
            *self.created_image.lock().unwrap() = Some(params.image.clone());
            *self.created_port.lock().unwrap() = Some(params.container_port);
            Ok(self.container_id.clone())
        }

        async fn start_container(&self, _id_or_name: &str) -> Result<(), String> {
            self.maybe_fail("start")?;
            *self.started.lock().unwrap() = true;
            Ok(())
        }

        async fn remove_container(&self, _id_or_name: &str, _force: bool) -> Result<(), String> {
            self.record("remove");
            self.remove_calls.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }

        async fn inspect_container(
            &self,
            id_or_name: &str,
        ) -> Result<ContainerInspectInfo, String> {
            self.record("inspect");
            if self.missing {
                return Err(format!("No such container: {id_or_name}"));
            }
            if self.fail_op == Some("inspect") {
                return Err("stub: inspect failed".into());
            }

            let name = self.created_name.lock().unwrap().clone();
            let expected = name.as_deref().unwrap_or("");
            if id_or_name != self.container_id.as_str()
                && id_or_name != expected
                && !expected.is_empty()
            {
                // Allow inspect by name after create.
                if name.as_deref() != Some(id_or_name) {
                    return Err(format!("No such container: {id_or_name}"));
                }
            }
            if name.is_none() && id_or_name != self.container_id {
                return Err(format!("No such container: {id_or_name}"));
            }

            let port = self.created_port.lock().unwrap().unwrap_or(8080);
            let mut port_bindings = HashMap::new();
            port_bindings.insert(format!("{port}/tcp"), vec![self.host_port]);

            Ok(ContainerInspectInfo {
                id: self.container_id.clone(),
                image: self.created_image.lock().unwrap().clone(),
                state: if *self.started.lock().unwrap() {
                    "running".into()
                } else {
                    "created".into()
                },
                port_bindings,
                labels: Some(self.created_labels.lock().unwrap().clone()),
                ip_address: Some("172.17.0.2".into()),
                restart_count: 0,
            })
        }

        async fn list_managed_containers(&self) -> Result<Vec<ContainerInspectInfo>, String> {
            if self.missing {
                return Ok(Vec::new());
            }
            let name = self.created_name.lock().unwrap().clone();
            if name.is_none() {
                return Ok(Vec::new());
            }
            match self.inspect_container(&self.container_id).await {
                Ok(info) => Ok(vec![info]),
                Err(_) => Ok(Vec::new()),
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
