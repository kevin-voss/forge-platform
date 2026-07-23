use crate::discovery::{DiscoveryClient, RegisterRequest, RenewRequest};
use crate::docker::{ContainerInspectInfo, DockerEngine};
use crate::network::{is_overlay_ip, is_provider_public_ip, NetworkClient};
use crate::status::{
    derive_status, DeriveInputs, DockerState, LastProbe, StatusView, WorkloadStatus,
};
use crate::workload::{container_name, DEPLOYMENT_ID_LABEL, MANAGED_LABEL, MANAGED_LABEL_VALUE};
use chrono::{DateTime, Utc};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::task::JoinHandle;
use tracing::{debug, info, warn};

/// Probe loop configuration.
#[derive(Debug, Clone)]
pub struct ProbeConfig {
    pub interval: Duration,
    pub timeout: Duration,
    pub failure_threshold: u32,
    pub ready_path: String,
    pub live_path: String,
    /// Host used with the published host port (e.g. `127.0.0.1` or `host.docker.internal`).
    pub probe_host: String,
}

impl Default for ProbeConfig {
    fn default() -> Self {
        Self {
            interval: Duration::from_secs(5),
            timeout: Duration::from_secs(2),
            failure_threshold: 3,
            ready_path: "/health/ready".into(),
            live_path: "/health/live".into(),
            probe_host: "127.0.0.1".into(),
        }
    }
}

#[derive(Debug, Clone)]
struct StatusEntry {
    deployment_id: String,
    status: WorkloadStatus,
    since: DateTime<Utc>,
    last_probe: LastProbe,
    restarts: u32,
    consecutive_live_failures: u32,
    stopped_by_operator: bool,
    host_port: Option<u16>,
    container_port: Option<u16>,
    container_ip: Option<String>,
    container_id: Option<String>,
}

impl StatusEntry {
    fn new(deployment_id: impl Into<String>) -> Self {
        let now = Utc::now();
        Self {
            deployment_id: deployment_id.into(),
            status: WorkloadStatus::Starting,
            since: now,
            last_probe: LastProbe {
                live: false,
                ready: false,
                at: now,
            },
            restarts: 0,
            consecutive_live_failures: 0,
            stopped_by_operator: false,
            host_port: None,
            container_port: None,
            container_ip: None,
            container_id: None,
        }
    }

    fn view(&self) -> StatusView {
        StatusView {
            deployment_id: self.deployment_id.clone(),
            status: self.status,
            since: self.since,
            last_probe: self.last_probe.clone(),
            restarts: self.restarts,
        }
    }
}

/// Bookkeeping fields shared by upsert / probe apply.
#[derive(Debug, Clone, Default)]
struct WorkloadMeta {
    host_port: Option<u16>,
    container_port: Option<u16>,
    container_ip: Option<String>,
    container_id: Option<String>,
    restarts: u32,
}

#[derive(Debug, Clone)]
struct ProbeApply {
    docker_state: DockerState,
    live_ok: bool,
    ready_ok: bool,
    failure_threshold: u32,
    meta: WorkloadMeta,
}

/// In-memory status cache keyed by deployment id.
#[derive(Debug, Default)]
pub struct StatusCache {
    inner: Mutex<HashMap<String, StatusEntry>>,
}

impl StatusCache {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn get(&self, deployment_id: &str) -> Option<StatusView> {
        self.inner
            .lock()
            .expect("status cache")
            .get(deployment_id)
            .map(StatusEntry::view)
    }

    /// Seed or refresh bookkeeping for a known workload (create path / rediscovery).
    fn upsert_workload(&self, deployment_id: &str, meta: WorkloadMeta, stopped_by_operator: bool) {
        let mut guard = self.inner.lock().expect("status cache");
        let entry = guard
            .entry(deployment_id.to_string())
            .or_insert_with(|| StatusEntry::new(deployment_id));
        Self::merge_meta(entry, meta);
        entry.stopped_by_operator = stopped_by_operator;
    }

    fn merge_meta(entry: &mut StatusEntry, meta: WorkloadMeta) {
        if let Some(hp) = meta.host_port {
            entry.host_port = Some(hp);
        }
        if let Some(cp) = meta.container_port {
            entry.container_port = Some(cp);
        }
        if meta.container_ip.is_some() {
            entry.container_ip = meta.container_ip;
        }
        if meta.container_id.is_some() {
            entry.container_id = meta.container_id;
        }
        entry.restarts = meta.restarts;
    }

    /// Mark operator-initiated stop (used by stop/delete).
    pub fn mark_stopped_by_operator(&self, deployment_id: &str) {
        let mut guard = self.inner.lock().expect("status cache");
        if let Some(entry) = guard.get_mut(deployment_id) {
            let old = entry.status;
            entry.stopped_by_operator = true;
            let new = WorkloadStatus::Stopped;
            if old != new {
                entry.status = new;
                entry.since = Utc::now();
                info!(
                    deployment_id = %deployment_id,
                    old = %old,
                    new = %new,
                    reason = "operator_stop",
                    "workload status transition"
                );
            }
        }
    }

    /// Drop a workload from the cache after delete.
    pub fn remove(&self, deployment_id: &str) {
        self.inner
            .lock()
            .expect("status cache")
            .remove(deployment_id);
    }

    fn apply_probe_result(&self, deployment_id: &str, apply: ProbeApply) {
        let mut guard = self.inner.lock().expect("status cache");
        let entry = guard
            .entry(deployment_id.to_string())
            .or_insert_with(|| StatusEntry::new(deployment_id));

        Self::merge_meta(entry, apply.meta);

        if apply.live_ok {
            entry.consecutive_live_failures = 0;
        } else if matches!(apply.docker_state, DockerState::Running) {
            entry.consecutive_live_failures = entry.consecutive_live_failures.saturating_add(1);
        }

        let now = Utc::now();
        entry.last_probe = LastProbe {
            live: apply.live_ok,
            ready: apply.ready_ok,
            at: now,
        };

        let new_status = derive_status(DeriveInputs {
            docker_state: apply.docker_state,
            live_ok: apply.live_ok,
            ready_ok: apply.ready_ok,
            consecutive_live_failures: entry.consecutive_live_failures,
            failure_threshold: apply.failure_threshold,
            stopped_by_operator: entry.stopped_by_operator,
        });

        if entry.status != new_status {
            let old = entry.status;
            entry.status = new_status;
            entry.since = now;
            info!(
                deployment_id = %deployment_id,
                old = %old,
                new = %new_status,
                live = apply.live_ok,
                ready = apply.ready_ok,
                consecutive_live_failures = entry.consecutive_live_failures,
                docker_state = ?apply.docker_state,
                "workload status transition"
            );
        }
    }

    #[cfg(test)]
    fn consecutive_failures(&self, deployment_id: &str) -> u32 {
        self.inner
            .lock()
            .expect("status cache")
            .get(deployment_id)
            .map(|e| e.consecutive_live_failures)
            .unwrap_or(0)
    }
}

/// HTTP probe client for workload health endpoints.
#[derive(Clone)]
pub struct ProbeClient {
    client: reqwest::Client,
    timeout: Duration,
    ready_path: String,
    live_path: String,
    probe_host: String,
}

impl ProbeClient {
    pub fn new(cfg: &ProbeConfig) -> Result<Self, String> {
        let client = reqwest::Client::builder()
            .timeout(cfg.timeout)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|e| format!("probe client build failed: {e}"))?;
        Ok(Self {
            client,
            timeout: cfg.timeout,
            ready_path: normalize_path(&cfg.ready_path),
            live_path: normalize_path(&cfg.live_path),
            probe_host: cfg.probe_host.clone(),
        })
    }

    async fn probe_ok(&self, base: &str, path: &str) -> bool {
        let url = format!("{base}{path}");
        match self.client.get(&url).send().await {
            Ok(resp) => resp.status().as_u16() == 200,
            Err(err) => {
                debug!(url = %url, error = %err, "probe request failed");
                false
            }
        }
    }

    /// Probe live + ready against the best available base URL.
    pub async fn probe(
        &self,
        host_port: Option<u16>,
        container_ip: Option<&str>,
        container_port: Option<u16>,
    ) -> (bool, bool) {
        let base = probe_base(&self.probe_host, host_port, container_ip, container_port);
        let Some(base) = base else {
            return (false, false);
        };
        // Bound overall probe pair by timeout (client also has per-request timeout).
        let live = self.probe_ok(&base, &self.live_path).await;
        let ready = if live {
            self.probe_ok(&base, &self.ready_path).await
        } else {
            // No need to hit ready if live already failed; still count as not ready.
            false
        };
        let _ = self.timeout;
        (live, ready)
    }
}

fn normalize_path(path: &str) -> String {
    let trimmed = path.trim();
    if trimmed.is_empty() {
        return "/".into();
    }
    if trimmed.starts_with('/') {
        trimmed.to_string()
    } else {
        format!("/{trimmed}")
    }
}

/// Prefer container IP:containerPort (sibling reachability); fall back to probe_host:hostPort.
fn probe_base(
    probe_host: &str,
    host_port: Option<u16>,
    container_ip: Option<&str>,
    container_port: Option<u16>,
) -> Option<String> {
    if let (Some(ip), Some(port)) = (container_ip, container_port) {
        if !ip.is_empty() && port > 0 {
            return Some(format!("http://{ip}:{port}"));
        }
    }
    let hp = host_port.filter(|p| *p > 0)?;
    let host = probe_host.trim();
    if host.is_empty() {
        return None;
    }
    Some(format!("http://{host}:{hp}"))
}

/// Overlay address publication settings (22.06).
#[derive(Debug, Clone)]
pub struct OverlayRegisterConfig {
    pub network_name: String,
    pub node_id: String,
    pub overlay_cidr: String,
    pub enabled: bool,
}

/// Periodic prober over all managed workloads.
pub struct Prober {
    docker: Arc<dyn DockerEngine>,
    cache: Arc<StatusCache>,
    client: ProbeClient,
    cfg: ProbeConfig,
    discovery: Option<Arc<DiscoveryClient>>,
    /// Defaults used when workload labels omit project/environment/service.
    discovery_defaults: DiscoveryDefaults,
    /// Optional forge-network client for overlay workload leases (22.06).
    network: Option<Arc<NetworkClient>>,
    overlay: Option<OverlayRegisterConfig>,
    /// workload_id → overlay IP cache
    overlay_ips: Mutex<HashMap<String, String>>,
}

/// Fallback scope for Discovery registration when labels are absent.
#[derive(Debug, Clone)]
pub struct DiscoveryDefaults {
    pub project: String,
    pub environment: String,
}

impl Default for DiscoveryDefaults {
    fn default() -> Self {
        Self {
            project: "demo".into(),
            environment: "local".into(),
        }
    }
}

impl Prober {
    pub fn new(
        docker: Arc<dyn DockerEngine>,
        cache: Arc<StatusCache>,
        cfg: ProbeConfig,
    ) -> Result<Self, String> {
        let client = ProbeClient::new(&cfg)?;
        Ok(Self {
            docker,
            cache,
            client,
            cfg,
            discovery: None,
            discovery_defaults: DiscoveryDefaults::default(),
            network: None,
            overlay: None,
            overlay_ips: Mutex::new(HashMap::new()),
        })
    }

    pub fn with_discovery(
        mut self,
        discovery: Arc<DiscoveryClient>,
        defaults: DiscoveryDefaults,
    ) -> Self {
        self.discovery = Some(discovery);
        self.discovery_defaults = defaults;
        self
    }

    pub fn with_overlay(
        mut self,
        network: Arc<NetworkClient>,
        overlay: OverlayRegisterConfig,
    ) -> Self {
        self.network = Some(network);
        self.overlay = Some(overlay);
        self
    }

    pub fn cache(&self) -> Arc<StatusCache> {
        Arc::clone(&self.cache)
    }

    pub fn discovery(&self) -> Option<Arc<DiscoveryClient>> {
        self.discovery.as_ref().map(Arc::clone)
    }

    /// Rediscover managed containers and seed the cache (service restart safety).
    pub async fn rediscover(&self) {
        match self.docker.list_managed_containers().await {
            Ok(list) => {
                info!(
                    count = list.len(),
                    "rediscovering managed workloads for probing"
                );
                for info in list {
                    if let Some(dep) = deployment_id_from(&info) {
                        self.cache
                            .upsert_workload(&dep, meta_from_inspect(&info), false);
                    }
                }
            }
            Err(err) => {
                warn!(error = %err, "failed to list managed containers for rediscovery");
            }
        }
    }

    /// One probe cycle over rediscovered + cached workloads.
    pub async fn tick_once(&self) {
        let mut targets: HashMap<String, ()> = HashMap::new();

        match self.docker.list_managed_containers().await {
            Ok(list) => {
                for info in list {
                    if let Some(dep) = deployment_id_from(&info) {
                        targets.insert(dep.clone(), ());
                        self.probe_one(&dep, Some(&info)).await;
                    }
                }
            }
            Err(err) => {
                warn!(error = %err, "prober list_managed_containers failed");
            }
        }

        // Also probe cache entries that may have been seeded by create before list catches up.
        let cached: Vec<String> = self
            .cache
            .inner
            .lock()
            .expect("status cache")
            .keys()
            .cloned()
            .collect();
        for dep in cached {
            if targets.contains_key(&dep) {
                continue;
            }
            self.probe_one(&dep, None).await;
        }
    }

    async fn probe_one(&self, deployment_id: &str, listed: Option<&ContainerInspectInfo>) {
        let result = async {
            let info = match listed {
                Some(i) => i.clone(),
                None => {
                    let name = container_name(deployment_id);
                    self.docker.inspect_container(&name).await?
                }
            };

            if let Some(labels) = &info.labels {
                if labels.get(MANAGED_LABEL).map(String::as_str) != Some(MANAGED_LABEL_VALUE) {
                    return Err(format!(
                        "container for {deployment_id} is not forge-managed"
                    ));
                }
            }

            let docker_state = DockerState::parse(&info.state);
            let (host_port, container_port) = ports_from(&info);
            let container_ip = info.ip_address.clone();

            let (live_ok, ready_ok) = if matches!(docker_state, DockerState::Running) {
                self.client
                    .probe(host_port, container_ip.as_deref(), container_port)
                    .await
            } else {
                (false, false)
            };

            self.cache.apply_probe_result(
                deployment_id,
                ProbeApply {
                    docker_state,
                    live_ok,
                    ready_ok,
                    failure_threshold: self.cfg.failure_threshold,
                    meta: WorkloadMeta {
                        host_port,
                        container_port,
                        container_ip: container_ip.clone(),
                        container_id: Some(info.id.clone()),
                        restarts: info.restart_count,
                    },
                },
            );
            self.sync_discovery(deployment_id, &info, ready_ok).await;
            Ok::<(), String>(())
        }
        .await;

        if let Err(err) = result {
            // Missing container → failed (unless operator-stopped).
            let missing = err.contains("No such container") || err.contains("not found");
            if missing {
                self.cache.apply_probe_result(
                    deployment_id,
                    ProbeApply {
                        docker_state: DockerState::Dead,
                        live_ok: false,
                        ready_ok: false,
                        failure_threshold: self.cfg.failure_threshold,
                        meta: WorkloadMeta::default(),
                    },
                );
            } else {
                warn!(
                    deployment_id = %deployment_id,
                    error = %err,
                    "per-workload probe error isolated"
                );
            }
        }
    }

    /// Ensure a workload has a cache entry and return current status (may be pre-probe).
    pub async fn status_for(&self, deployment_id: &str) -> Result<StatusView, String> {
        if let Some(view) = self.cache.get(deployment_id) {
            return Ok(view);
        }

        let name = container_name(deployment_id);
        let info = self.docker.inspect_container(&name).await.map_err(|err| {
            if err.contains("No such container") || err.contains("not found") {
                format!("no workload for deployment_id {deployment_id}")
            } else {
                err
            }
        })?;

        if let Some(labels) = &info.labels {
            if labels.get(MANAGED_LABEL).map(String::as_str) != Some(MANAGED_LABEL_VALUE) {
                return Err(format!(
                    "no managed workload for deployment_id {deployment_id}"
                ));
            }
        }

        self.cache
            .upsert_workload(deployment_id, meta_from_inspect(&info), false);
        // Immediate probe so GET after create is useful.
        self.probe_one(deployment_id, Some(&info)).await;
        self.cache
            .get(deployment_id)
            .ok_or_else(|| format!("status missing after probe for {deployment_id}"))
    }

    async fn sync_discovery(
        &self,
        deployment_id: &str,
        info: &ContainerInspectInfo,
        ready_ok: bool,
    ) {
        let Some(discovery) = self.discovery.as_ref() else {
            return;
        };
        if !discovery.enabled() {
            return;
        }

        let labels = info.labels.as_ref();
        let project = label_or(labels, "forge.project", &self.discovery_defaults.project);
        let environment = label_or(
            labels,
            "forge.environment",
            &self.discovery_defaults.environment,
        );
        let service = label_or(labels, "forge.service", deployment_id);
        let address_port = ports_from(info)
            .1
            .or(ports_from(info).0)
            .unwrap_or(8080);

        let Some(address_ip) = self.resolve_register_address(deployment_id, info).await else {
            // No current overlay lease → exclude from Discovery answers.
            return;
        };

        if !discovery.is_registered(deployment_id) {
            if !ready_ok {
                return;
            }
            if let Err(err) = discovery
                .register(RegisterRequest {
                    project: project.clone(),
                    environment: environment.clone(),
                    service: service.clone(),
                    id: deployment_id.to_string(),
                    address_ip,
                    address_port,
                    protocol: "http".into(),
                    revision: labels.and_then(|l| l.get("forge.revision").cloned()),
                })
                .await
            {
                warn!(
                    deployment_id = %deployment_id,
                    error = %err,
                    "discovery register failed"
                );
                return;
            }
        }

        if let Err(err) = discovery
            .renew(RenewRequest {
                project,
                environment,
                id: deployment_id.to_string(),
                ready: ready_ok,
            })
            .await
        {
            warn!(
                deployment_id = %deployment_id,
                error = %err,
                "discovery renew failed"
            );
        }
    }

    /// Prefer a Network overlay lease; fall back to Docker IP only when overlay
    /// registration is disabled. Never publish provider public IPs.
    async fn resolve_register_address(
        &self,
        deployment_id: &str,
        info: &ContainerInspectInfo,
    ) -> Option<String> {
        if let (Some(network), Some(overlay)) = (&self.network, &self.overlay) {
            if overlay.enabled {
                if let Some(cached) = self
                    .overlay_ips
                    .lock()
                    .expect("overlay ips")
                    .get(deployment_id)
                    .cloned()
                {
                    return Some(cached);
                }
                match network
                    .allocate_workload_lease(
                        &overlay.network_name,
                        &overlay.node_id,
                        deployment_id,
                    )
                    .await
                {
                    Ok(lease) => {
                        if is_provider_public_ip(&lease.address)
                            || !is_overlay_ip(&lease.address, &overlay.overlay_cidr)
                        {
                            warn!(
                                deployment_id = %deployment_id,
                                address = %lease.address,
                                "overlay lease outside cluster CIDR; excluding from discovery"
                            );
                            return None;
                        }
                        info!(
                            event = "runtime.discovery.overlay_address",
                            deployment_id = %deployment_id,
                            overlay_ip = %lease.address,
                            "publishing overlay address to discovery"
                        );
                        self.overlay_ips
                            .lock()
                            .expect("overlay ips")
                            .insert(deployment_id.to_string(), lease.address.clone());
                        return Some(lease.address);
                    }
                    Err(err) => {
                        // Node may not have a network lease yet (join in progress) or
                        // docker-only demos without forge-network — fall back to Docker IP.
                        warn!(
                            deployment_id = %deployment_id,
                            error = %err,
                            "overlay lease allocate failed; falling back to container IP"
                        );
                    }
                }
            }
        }

        let docker_ip = info
            .ip_address
            .clone()
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "127.0.0.1".into());
        if is_provider_public_ip(&docker_ip) {
            warn!(
                deployment_id = %deployment_id,
                address = %docker_ip,
                "refusing to register provider public IP with discovery"
            );
            return None;
        }
        Some(docker_ip)
    }

    /// Spawn a supervised probe loop. Per-workload errors never crash the process.
    pub fn spawn(self: &Arc<Self>) -> JoinHandle<()> {
        let state = Arc::clone(self);
        let interval = self.cfg.interval;
        tokio::spawn(async move {
            // Initial rediscovery + tick.
            state.rediscover().await;
            state.tick_once().await;

            let mut ticker = tokio::time::interval(interval);
            ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
            // Skip the immediate first tick (already ran above).
            ticker.tick().await;

            loop {
                ticker.tick().await;
                let inner = Arc::clone(&state);
                let result = tokio::task::spawn(async move {
                    inner.tick_once().await;
                })
                .await;
                if let Err(err) = result {
                    warn!(error = %err, "prober task panicked; continuing");
                }
            }
        })
    }
}

fn deployment_id_from(info: &ContainerInspectInfo) -> Option<String> {
    info.labels
        .as_ref()
        .and_then(|l| l.get(DEPLOYMENT_ID_LABEL).cloned())
        .filter(|s| !s.is_empty())
}

fn label_or(
    labels: Option<&HashMap<String, String>>,
    key: &str,
    default: &str,
) -> String {
    labels
        .and_then(|l| l.get(key).cloned())
        .filter(|s| !s.trim().is_empty())
        .unwrap_or_else(|| default.to_string())
}

fn ports_from(info: &ContainerInspectInfo) -> (Option<u16>, Option<u16>) {
    let container_port = info
        .port_bindings
        .keys()
        .find_map(|k| k.strip_suffix("/tcp").and_then(|p| p.parse::<u16>().ok()));
    let host_port = container_port
        .and_then(|cp| {
            let key = format!("{cp}/tcp");
            info.port_bindings
                .get(&key)
                .and_then(|ports| ports.first().copied())
        })
        .or_else(|| info.port_bindings.values().flatten().copied().next());
    (host_port, container_port)
}

fn meta_from_inspect(info: &ContainerInspectInfo) -> WorkloadMeta {
    let (host_port, container_port) = ports_from(info);
    WorkloadMeta {
        host_port,
        container_port,
        container_ip: info.ip_address.clone(),
        container_id: Some(info.id.clone()),
        restarts: info.restart_count,
    }
}

/// Notify the cache that a workload was just created (starts as `starting`).
pub fn note_workload_created(
    cache: &StatusCache,
    deployment_id: &str,
    host_port: u16,
    container_port: u16,
    container_id: &str,
) {
    cache.upsert_workload(
        deployment_id,
        WorkloadMeta {
            host_port: Some(host_port),
            container_port: Some(container_port),
            container_ip: None,
            container_id: Some(container_id.to_string()),
            restarts: 0,
        },
        false,
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::RecordingDocker;
    use crate::docker::{CreateWorkloadParams, DockerEngine};
    use crate::workload::workload_labels;
    use async_trait::async_trait;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Mutex as StdMutex;

    #[test]
    fn probe_base_prefers_container_ip() {
        let base = probe_base("127.0.0.1", Some(49152), Some("172.17.0.2"), Some(8080));
        assert_eq!(base.as_deref(), Some("http://172.17.0.2:8080"));
    }

    #[test]
    fn probe_base_falls_back_to_host_port() {
        let base = probe_base("host.docker.internal", Some(49152), None, Some(8080));
        assert_eq!(base.as_deref(), Some("http://host.docker.internal:49152"));
    }

    #[tokio::test]
    async fn failure_threshold_marks_unhealthy() {
        let cache = StatusCache::new();
        let meta = WorkloadMeta {
            host_port: Some(1),
            container_port: Some(8080),
            container_ip: None,
            container_id: Some("c1".into()),
            restarts: 0,
        };
        cache.upsert_workload("dep-1", meta.clone(), false);

        for i in 1..=3 {
            cache.apply_probe_result(
                "dep-1",
                ProbeApply {
                    docker_state: DockerState::Running,
                    live_ok: false,
                    ready_ok: false,
                    failure_threshold: 3,
                    meta: meta.clone(),
                },
            );
            let view = cache.get("dep-1").unwrap();
            if i < 3 {
                assert_eq!(view.status, WorkloadStatus::Starting, "i={i}");
            } else {
                assert_eq!(view.status, WorkloadStatus::Unhealthy, "i={i}");
            }
        }
        assert_eq!(cache.consecutive_failures("dep-1"), 3);
    }

    #[tokio::test]
    async fn ready_transition_from_starting() {
        let cache = StatusCache::new();
        let meta = WorkloadMeta {
            host_port: Some(1),
            container_port: Some(8080),
            container_ip: None,
            container_id: Some("c1".into()),
            restarts: 0,
        };
        cache.upsert_workload("dep-1", meta.clone(), false);
        cache.apply_probe_result(
            "dep-1",
            ProbeApply {
                docker_state: DockerState::Running,
                live_ok: true,
                ready_ok: true,
                failure_threshold: 3,
                meta,
            },
        );
        let view = cache.get("dep-1").unwrap();
        assert_eq!(view.status, WorkloadStatus::Ready);
        assert!(view.last_probe.live);
        assert!(view.last_probe.ready);
    }

    struct ListingDocker {
        inner: RecordingDocker,
        listed: StdMutex<Vec<ContainerInspectInfo>>,
        list_calls: AtomicUsize,
    }

    impl ListingDocker {
        fn with_workload(host_port: u16) -> Self {
            let labels = workload_labels("deployment-123", "node-1", None);
            let mut port_bindings = HashMap::new();
            port_bindings.insert("8080/tcp".into(), vec![host_port]);
            Self {
                inner: RecordingDocker::ok(host_port),
                listed: StdMutex::new(vec![ContainerInspectInfo {
                    id: "cid-1".into(),
                    image: Some("localhost:5000/demo-go:latest".into()),
                    state: "running".into(),
                    port_bindings,
                    labels: Some(labels),
                    ip_address: Some("172.17.0.9".into()),
                    restart_count: 0,
                }]),
                list_calls: AtomicUsize::new(0),
            }
        }
    }

    #[async_trait]
    impl crate::docker::DockerProbe for ListingDocker {
        async fn ping(&self) -> Result<(), String> {
            self.inner.ping().await
        }
        async fn engine_version(&self) -> Result<String, String> {
            self.inner.engine_version().await
        }
    }

    #[async_trait]
    impl DockerEngine for ListingDocker {
        async fn pull_image(&self, image: &str, timeout: Duration) -> Result<(), String> {
            self.inner.pull_image(image, timeout).await
        }
        async fn create_container(&self, params: &CreateWorkloadParams) -> Result<String, String> {
            self.inner.create_container(params).await
        }
        async fn start_container(&self, id_or_name: &str) -> Result<(), String> {
            self.inner.start_container(id_or_name).await
        }
        async fn stop_container(&self, id_or_name: &str, grace_seconds: u64) -> Result<(), String> {
            self.inner.stop_container(id_or_name, grace_seconds).await
        }
        async fn remove_container(&self, id_or_name: &str, force: bool) -> Result<(), String> {
            self.inner.remove_container(id_or_name, force).await
        }
        async fn inspect_container(
            &self,
            id_or_name: &str,
        ) -> Result<ContainerInspectInfo, String> {
            self.inner.inspect_container(id_or_name).await
        }
        async fn list_managed_containers(&self) -> Result<Vec<ContainerInspectInfo>, String> {
            self.list_calls.fetch_add(1, Ordering::SeqCst);
            Ok(self.listed.lock().unwrap().clone())
        }
        fn logs(
            &self,
            id_or_name: &str,
            options: &crate::docker::ContainerLogsOptions,
        ) -> crate::docker::LogChunkStream {
            self.inner.logs(id_or_name, options)
        }
    }

    #[tokio::test]
    async fn rediscover_seeds_cache() {
        let docker: Arc<dyn DockerEngine> = Arc::new(ListingDocker::with_workload(45555));
        let cache = Arc::new(StatusCache::new());
        let prober = Prober::new(docker, cache.clone(), ProbeConfig::default()).unwrap();
        prober.rediscover().await;
        let view = cache.get("deployment-123").expect("seeded");
        assert_eq!(view.deployment_id, "deployment-123");
        assert_eq!(view.status, WorkloadStatus::Starting);
    }
}
