use crate::converge::{LifecycleOwner, ReportMode};
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
    /// Optional Control base URL for desired-state poll + status report.
    pub control_url: Option<String>,
    /// Interval for Control desired→actual reconcile cycles.
    pub reconcile_interval: Duration,
    /// `push` reports status to Control; `pull` relies on `GET /v1/node/state`.
    pub control_report_mode: ReportMode,
    /// Who owns create/stop for desired deployments (`runtime` or `control`).
    pub lifecycle_owner: LifecycleOwner,
    /// Max time to wait for an image pull.
    pub pull_timeout: Duration,
    /// Informational default registry host (images are fully qualified).
    pub default_registry: String,
    pub probe_interval: Duration,
    pub probe_timeout: Duration,
    pub probe_failure_threshold: u32,
    pub probe_ready_path: String,
    pub probe_live_path: String,
    /// Host paired with published host ports for health probes.
    pub probe_host: String,
    /// Default `tail` for `GET /v1/workloads/{id}/logs` when omitted.
    pub log_default_tail: u32,
    /// Soft buffer size (bytes) used to size log-follow backpressure channels.
    pub log_stream_buffer: usize,
    /// Grace period before Docker escalates SIGTERM → SIGKILL on workload stop.
    pub stop_grace: Duration,
    /// How to handle create requests that conflict with an existing container's image.
    pub on_config_conflict: crate::lifecycle::OnConfigConflict,
    /// Optional stable node id override for multi-node demos (`node-a`).
    pub node_id: Option<String>,
    /// Advertised slot capacity when registering with Control.
    pub node_slots: u32,
    /// Capacity source: `host` (introspect) or `slots-only` (deterministic demos/CI).
    pub node_capacity_source: String,
    /// When true, apply workload `limits` as Docker resource constraints.
    pub enforce_limits: bool,
    /// Optional advertised address for Control registration.
    pub node_address: Option<String>,
    /// Operator labels `k=v,k2=v2` (`FORGE_NODE_LABELS`).
    pub node_labels: std::collections::HashMap<String, String>,
    /// Operator taints `key=value:Effect,...` (`FORGE_NODE_TAINTS`).
    pub node_taints: Vec<NodeTaintConfig>,
    /// Optional provider label value (`FORGE_NODE_PROVIDER`, default `docker`).
    pub node_provider: Option<String>,
    /// Topology zone (`FORGE_NODE_ZONE`, default `default`).
    pub node_zone: String,
    /// Topology region (`FORGE_NODE_REGION`, default `default`).
    pub node_region: String,
    /// Optional NodePool id (`FORGE_NODE_POOL_ID`).
    pub node_pool_id: Option<String>,
    /// Single-use bootstrap token for join handshake (`FORGE_NODE_BOOTSTRAP_TOKEN`).
    pub bootstrap_token: Option<String>,
    /// Directory for WireGuard key pair; defaults to `data_dir`.
    pub key_dir: Option<PathBuf>,
    /// Interval for Control register/heartbeat reporting.
    pub control_heartbeat_interval: Duration,
    /// Optional Discovery base URL for endpoint lease registration (epic 21.02).
    pub discovery_url: Option<String>,
    /// When false, Runtime skips Discovery register/renew/deregister.
    pub discovery_register_enabled: bool,
    /// Lease TTL advertised on register/renew.
    pub discovery_lease_seconds: u32,
    /// Default project when workload lacks `forge.project`.
    pub discovery_default_project: String,
    /// Default environment when workload lacks `forge.environment`.
    pub discovery_default_environment: String,
    /// Optional forge-network base URL for WireGuard peer poll (22.03).
    pub network_url: Option<String>,
    /// Network resource name for peer APIs.
    pub network_name: String,
    /// WireGuard backend: kernel|userspace|fake|auto.
    pub network_wg_backend: String,
    /// Local WireGuard interface name.
    pub network_wg_iface: String,
    /// Advertised WireGuard endpoint host:port (optional).
    pub network_wg_endpoint: Option<String>,
    /// Peer poll interval.
    pub network_peer_poll_interval: Duration,
    /// Same-Docker-daemon colocation flag (22.04); compose sets true for local nodes.
    pub node_docker_colocated: bool,
    /// Optional provider private-network membership tag (22.04).
    pub node_network_membership: Option<String>,
    /// NIC used for provider-private routes (22.04).
    pub network_private_iface: String,
    /// Route backend: host|fake (22.04).
    pub network_route_backend: String,
    /// Policy poll interval (22.05).
    pub network_policy_poll_interval: Duration,
    /// Policy backend: host|fake (22.05).
    pub network_policy_backend: String,
    /// Fraction of denies logged at detail level + emitted as events (22.05).
    pub network_deny_log_sample_rate: f64,
    /// Optional forge-events base URL for `network.policy.denied` (22.05).
    pub events_url: Option<String>,
    /// Overlay DNS nameserver IP (Discovery on the overlay / Compose fixed IP) (22.06).
    pub network_dns_nameserver: Option<String>,
    /// Authoritative zone (default `svc.forge`) (22.06).
    pub network_dns_zone: String,
    /// Resolver search domain, e.g. `production.shop.svc.forge` (22.06).
    pub network_dns_search: String,
    /// DNS config backend: host|fake (22.06).
    pub network_dns_backend: String,
    /// Path for forge-managed resolv snippet (22.06).
    pub network_dns_resolv_path: Option<PathBuf>,
    /// Cluster overlay CIDR used to reject public IPs in registration (22.06).
    pub network_overlay_cidr: String,
    /// Drift reconcile interval (22.06).
    pub network_drift_poll_interval: Duration,
    /// When true and network_url is set, register Discovery endpoints with overlay leases (22.06).
    pub network_overlay_register: bool,
}

/// Parsed Runtime taint from `FORGE_NODE_TAINTS` (`key=value:Effect` or `key:Effect`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NodeTaintConfig {
    pub key: String,
    pub value: Option<String>,
    pub effect: String,
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

        let reconcile_raw =
            env::var("FORGE_RECONCILE_INTERVAL_SECONDS").unwrap_or_else(|_| "10".into());
        let reconcile_secs: u64 = reconcile_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_RECONCILE_INTERVAL_SECONDS must be a positive integer, got {reconcile_raw:?}"
            )
        })?;
        if reconcile_secs == 0 {
            return Err(format!(
                "FORGE_RECONCILE_INTERVAL_SECONDS must be a positive integer, got {reconcile_raw:?}"
            ));
        }

        let report_mode_raw =
            env::var("FORGE_CONTROL_REPORT_MODE").unwrap_or_else(|_| "push".into());
        let control_report_mode = ReportMode::parse(&report_mode_raw)?;

        let lifecycle_owner_raw =
            env::var("FORGE_LIFECYCLE_OWNER").unwrap_or_else(|_| "runtime".into());
        let lifecycle_owner = LifecycleOwner::parse(&lifecycle_owner_raw)?;

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

        let probe_interval_raw =
            env::var("FORGE_PROBE_INTERVAL_SECONDS").unwrap_or_else(|_| "5".into());
        let probe_interval_secs: u64 = probe_interval_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_PROBE_INTERVAL_SECONDS must be a positive integer, got {probe_interval_raw:?}"
            )
        })?;
        if probe_interval_secs == 0 {
            return Err(format!(
                "FORGE_PROBE_INTERVAL_SECONDS must be a positive integer, got {probe_interval_raw:?}"
            ));
        }

        let probe_timeout_raw =
            env::var("FORGE_PROBE_TIMEOUT_SECONDS").unwrap_or_else(|_| "2".into());
        let probe_timeout_secs: u64 = probe_timeout_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_PROBE_TIMEOUT_SECONDS must be a positive integer, got {probe_timeout_raw:?}"
            )
        })?;
        if probe_timeout_secs == 0 {
            return Err(format!(
                "FORGE_PROBE_TIMEOUT_SECONDS must be a positive integer, got {probe_timeout_raw:?}"
            ));
        }

        let threshold_raw =
            env::var("FORGE_PROBE_FAILURE_THRESHOLD").unwrap_or_else(|_| "3".into());
        let probe_failure_threshold: u32 = threshold_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_PROBE_FAILURE_THRESHOLD must be a positive integer, got {threshold_raw:?}"
            )
        })?;
        if probe_failure_threshold == 0 {
            return Err(format!(
                "FORGE_PROBE_FAILURE_THRESHOLD must be a positive integer, got {threshold_raw:?}"
            ));
        }

        let probe_ready_path = non_empty_env("FORGE_PROBE_READY_PATH", "/health/ready");
        let probe_live_path = non_empty_env("FORGE_PROBE_LIVE_PATH", "/health/live");
        let probe_host = non_empty_env("FORGE_PROBE_HOST", "127.0.0.1");

        let log_tail_raw = env::var("FORGE_LOG_DEFAULT_TAIL").unwrap_or_else(|_| "100".into());
        let log_default_tail: u32 = log_tail_raw.trim().parse().map_err(|_| {
            format!("FORGE_LOG_DEFAULT_TAIL must be a non-negative integer, got {log_tail_raw:?}")
        })?;

        let log_buf_raw = env::var("FORGE_LOG_STREAM_BUFFER").unwrap_or_else(|_| "8192".into());
        let log_stream_buffer: usize = log_buf_raw.trim().parse().map_err(|_| {
            format!("FORGE_LOG_STREAM_BUFFER must be a positive integer, got {log_buf_raw:?}")
        })?;
        if log_stream_buffer == 0 {
            return Err(format!(
                "FORGE_LOG_STREAM_BUFFER must be a positive integer, got {log_buf_raw:?}"
            ));
        }

        let stop_grace_raw = env::var("FORGE_STOP_GRACE_SECONDS").unwrap_or_else(|_| "10".into());
        let stop_grace_secs: u64 = stop_grace_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_STOP_GRACE_SECONDS must be a non-negative integer, got {stop_grace_raw:?}"
            )
        })?;

        let conflict_raw =
            env::var("FORGE_ON_CONFIG_CONFLICT").unwrap_or_else(|_| "recreate".into());
        let on_config_conflict = crate::lifecycle::OnConfigConflict::parse(&conflict_raw)?;

        let node_id = env::var("FORGE_NODE_ID")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let slots_raw = env::var("FORGE_NODE_SLOTS").unwrap_or_else(|_| "4".into());
        let node_slots: u32 = slots_raw.trim().parse().map_err(|_| {
            format!("FORGE_NODE_SLOTS must be a positive integer, got {slots_raw:?}")
        })?;
        if node_slots == 0 {
            return Err(format!(
                "FORGE_NODE_SLOTS must be a positive integer, got {slots_raw:?}"
            ));
        }

        let node_capacity_source = env::var("FORGE_NODE_CAPACITY_SOURCE")
            .unwrap_or_else(|_| "host".into())
            .trim()
            .to_ascii_lowercase();
        if node_capacity_source != "host" && node_capacity_source != "slots-only" {
            return Err(format!(
                "FORGE_NODE_CAPACITY_SOURCE must be host|slots-only, got {node_capacity_source:?}"
            ));
        }

        let enforce_limits_raw = env::var("FORGE_ENFORCE_LIMITS")
            .unwrap_or_else(|_| "true".into())
            .trim()
            .to_ascii_lowercase();
        let enforce_limits = match enforce_limits_raw.as_str() {
            "true" | "1" | "yes" => true,
            "false" | "0" | "no" => false,
            other => {
                return Err(format!(
                    "FORGE_ENFORCE_LIMITS must be true|false, got {other:?}"
                ));
            }
        };

        let node_address = env::var("FORGE_NODE_ADDRESS")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let node_labels = parse_node_labels(env::var("FORGE_NODE_LABELS").ok().as_deref())?;
        let node_taints = parse_node_taints(env::var("FORGE_NODE_TAINTS").ok().as_deref())?;
        let node_provider = env::var("FORGE_NODE_PROVIDER")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .or_else(|| Some("docker".to_string()));
        let node_zone = non_empty_env("FORGE_NODE_ZONE", "default");
        let node_region = non_empty_env("FORGE_NODE_REGION", "default");
        let node_pool_id = env::var("FORGE_NODE_POOL_ID")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let bootstrap_token = env::var("FORGE_NODE_BOOTSTRAP_TOKEN")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let key_dir = env::var("FORGE_NODE_KEY_DIR")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .map(PathBuf::from);

        let control_hb_ms_raw =
            env::var("FORGE_HEARTBEAT_INTERVAL_MS").unwrap_or_else(|_| "5000".into());
        let control_hb_ms: u64 = control_hb_ms_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_HEARTBEAT_INTERVAL_MS must be a positive integer, got {control_hb_ms_raw:?}"
            )
        })?;
        if control_hb_ms == 0 {
            return Err(format!(
                "FORGE_HEARTBEAT_INTERVAL_MS must be a positive integer, got {control_hb_ms_raw:?}"
            ));
        }

        let discovery_url = env::var("FORGE_DISCOVERY_URL")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let discovery_register_enabled = match env::var("FORGE_DISCOVERY_REGISTER_ENABLED")
            .unwrap_or_else(|_| "true".into())
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "false" | "0" | "no" => false,
            _ => true,
        };
        let lease_raw =
            env::var("FORGE_DISCOVERY_LEASE_SECONDS").unwrap_or_else(|_| "20".into());
        let discovery_lease_seconds: u32 = lease_raw.trim().parse().map_err(|_| {
            format!("FORGE_DISCOVERY_LEASE_SECONDS must be a positive integer, got {lease_raw:?}")
        })?;
        if discovery_lease_seconds == 0 {
            return Err(format!(
                "FORGE_DISCOVERY_LEASE_SECONDS must be a positive integer, got {lease_raw:?}"
            ));
        }
        let discovery_default_project =
            non_empty_env("FORGE_DISCOVERY_DEFAULT_PROJECT", "demo");
        let discovery_default_environment =
            non_empty_env("FORGE_DISCOVERY_DEFAULT_ENVIRONMENT", "local");

        let network_url = env::var("FORGE_NETWORK_URL")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let network_name = non_empty_env("FORGE_NETWORK_NAME", "cluster-overlay");
        let network_wg_backend = non_empty_env("FORGE_NETWORK_WG_BACKEND", "auto");
        let _ = crate::network::WgBackendKind::parse(&network_wg_backend)?;
        let network_wg_iface = non_empty_env("FORGE_NETWORK_WG_IFACE", "wg0");
        let network_wg_endpoint = env::var("FORGE_NETWORK_WG_ENDPOINT")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let peer_poll_raw =
            env::var("FORGE_NETWORK_PEER_POLL_INTERVAL_S").unwrap_or_else(|_| "5".into());
        let peer_poll_secs: u64 = peer_poll_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_NETWORK_PEER_POLL_INTERVAL_S must be a positive integer, got {peer_poll_raw:?}"
            )
        })?;
        if peer_poll_secs == 0 {
            return Err(format!(
                "FORGE_NETWORK_PEER_POLL_INTERVAL_S must be a positive integer, got {peer_poll_raw:?}"
            ));
        }

        let node_docker_colocated = match env::var("FORGE_NODE_DOCKER_COLOCATED")
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "1" | "true" | "yes" => true,
            _ => false,
        };
        let node_network_membership = env::var("FORGE_NODE_NETWORK_MEMBERSHIP")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let network_private_iface =
            non_empty_env("FORGE_NETWORK_PRIVATE_IFACE", "eth0");
        let network_route_backend =
            non_empty_env("FORGE_NETWORK_ROUTE_BACKEND", "host");

        let policy_poll_raw =
            env::var("FORGE_NETWORK_POLICY_POLL_INTERVAL_S").unwrap_or_else(|_| "5".into());
        let policy_poll_secs: u64 = policy_poll_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_NETWORK_POLICY_POLL_INTERVAL_S must be a positive integer, got {policy_poll_raw:?}"
            )
        })?;
        if policy_poll_secs == 0 {
            return Err(format!(
                "FORGE_NETWORK_POLICY_POLL_INTERVAL_S must be a positive integer, got {policy_poll_raw:?}"
            ));
        }
        let network_policy_backend =
            non_empty_env("FORGE_NETWORK_POLICY_BACKEND", "host");
        let sample_raw =
            env::var("FORGE_NETWORK_DENY_LOG_SAMPLE_RATE").unwrap_or_else(|_| "0.1".into());
        let network_deny_log_sample_rate: f64 = sample_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_NETWORK_DENY_LOG_SAMPLE_RATE must be a float 0.0–1.0, got {sample_raw:?}"
            )
        })?;
        if !(0.0..=1.0).contains(&network_deny_log_sample_rate) {
            return Err(format!(
                "FORGE_NETWORK_DENY_LOG_SAMPLE_RATE must be a float 0.0–1.0, got {sample_raw:?}"
            ));
        }
        let events_url = env::var("FORGE_EVENTS_URL")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());

        let network_dns_nameserver = env::var("FORGE_NETWORK_DNS_NAMESERVER")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty());
        let network_dns_zone = non_empty_env("FORGE_NETWORK_DNS_ZONE", "svc.forge");
        let network_dns_search = env::var("FORGE_NETWORK_DNS_SEARCH")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| {
                format!(
                    "{}.{}.{}",
                    discovery_default_environment, discovery_default_project, network_dns_zone
                )
            });
        let network_dns_backend = non_empty_env("FORGE_NETWORK_DNS_BACKEND", "host");
        let network_dns_resolv_path = env::var("FORGE_NETWORK_DNS_RESOLV_PATH")
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .map(PathBuf::from);
        let network_overlay_cidr =
            non_empty_env("FORGE_NETWORK_OVERLAY_CIDR", "10.100.0.0/16");
        let drift_poll_raw =
            env::var("FORGE_NETWORK_DRIFT_POLL_INTERVAL_S").unwrap_or_else(|_| "15".into());
        let drift_poll_secs: u64 = drift_poll_raw.trim().parse().map_err(|_| {
            format!(
                "FORGE_NETWORK_DRIFT_POLL_INTERVAL_S must be a positive integer, got {drift_poll_raw:?}"
            )
        })?;
        if drift_poll_secs == 0 {
            return Err(format!(
                "FORGE_NETWORK_DRIFT_POLL_INTERVAL_S must be a positive integer, got {drift_poll_raw:?}"
            ));
        }
        let network_overlay_register = match env::var("FORGE_NETWORK_OVERLAY_REGISTER")
            .unwrap_or_else(|_| "true".into())
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "false" | "0" | "no" => false,
            _ => true,
        };

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
            reconcile_interval: Duration::from_secs(reconcile_secs),
            control_report_mode,
            lifecycle_owner,
            pull_timeout: Duration::from_secs(pull_secs),
            default_registry,
            probe_interval: Duration::from_secs(probe_interval_secs),
            probe_timeout: Duration::from_secs(probe_timeout_secs),
            probe_failure_threshold,
            probe_ready_path,
            probe_live_path,
            probe_host,
            log_default_tail,
            log_stream_buffer,
            stop_grace: Duration::from_secs(stop_grace_secs),
            on_config_conflict,
            node_id,
            node_slots,
            node_capacity_source,
            enforce_limits,
            node_address,
            node_labels,
            node_taints,
            node_provider,
            node_zone,
            node_region,
            node_pool_id,
            bootstrap_token,
            key_dir,
            control_heartbeat_interval: Duration::from_millis(control_hb_ms),
            discovery_url,
            discovery_register_enabled,
            discovery_lease_seconds,
            discovery_default_project,
            discovery_default_environment,
            network_url,
            network_name,
            network_wg_backend,
            network_wg_iface,
            network_wg_endpoint,
            network_peer_poll_interval: Duration::from_secs(peer_poll_secs),
            node_docker_colocated,
            node_network_membership,
            network_private_iface,
            network_route_backend,
            network_policy_poll_interval: Duration::from_secs(policy_poll_secs),
            network_policy_backend,
            network_deny_log_sample_rate,
            events_url,
            network_dns_nameserver,
            network_dns_zone,
            network_dns_search,
            network_dns_backend,
            network_dns_resolv_path,
            network_overlay_cidr,
            network_drift_poll_interval: Duration::from_secs(drift_poll_secs),
            network_overlay_register,
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

fn parse_node_labels(raw: Option<&str>) -> Result<std::collections::HashMap<String, String>, String> {
    let mut out = std::collections::HashMap::new();
    let Some(raw) = raw.map(str::trim).filter(|s| !s.is_empty()) else {
        return Ok(out);
    };
    for part in raw.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        let (k, v) = part.split_once('=').ok_or_else(|| {
            format!("FORGE_NODE_LABELS entry must be key=value, got {part:?}")
        })?;
        let k = k.trim();
        let v = v.trim();
        if k.is_empty() {
            return Err(format!(
                "FORGE_NODE_LABELS entry key must not be empty, got {part:?}"
            ));
        }
        out.insert(k.to_string(), v.to_string());
    }
    Ok(out)
}

fn parse_node_taints(raw: Option<&str>) -> Result<Vec<NodeTaintConfig>, String> {
    let mut out = Vec::new();
    let Some(raw) = raw.map(str::trim).filter(|s| !s.is_empty()) else {
        return Ok(out);
    };
    for part in raw.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        // Formats: key=value:Effect  OR  key:Effect
        let (left, effect) = part.split_once(':').ok_or_else(|| {
            format!("FORGE_NODE_TAINTS entry must be key[=value]:Effect, got {part:?}")
        })?;
        let effect = effect.trim();
        if effect != "NoSchedule" && effect != "NoExecute" {
            return Err(format!(
                "FORGE_NODE_TAINTS effect must be NoSchedule|NoExecute, got {effect:?}"
            ));
        }
        let (key, value) = if let Some((k, v)) = left.split_once('=') {
            (k.trim().to_string(), Some(v.trim().to_string()))
        } else {
            (left.trim().to_string(), None)
        };
        if key.is_empty() {
            return Err(format!(
                "FORGE_NODE_TAINTS entry key must not be empty, got {part:?}"
            ));
        }
        out.push(NodeTaintConfig {
            key,
            value,
            effect: effect.to_string(),
        });
    }
    Ok(out)
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
            "FORGE_RECONCILE_INTERVAL_SECONDS",
            "FORGE_CONTROL_REPORT_MODE",
            "FORGE_LIFECYCLE_OWNER",
            "FORGE_PULL_TIMEOUT_SECONDS",
            "FORGE_DEFAULT_REGISTRY",
            "FORGE_PROBE_INTERVAL_SECONDS",
            "FORGE_PROBE_TIMEOUT_SECONDS",
            "FORGE_PROBE_FAILURE_THRESHOLD",
            "FORGE_PROBE_READY_PATH",
            "FORGE_PROBE_LIVE_PATH",
            "FORGE_PROBE_HOST",
            "FORGE_LOG_DEFAULT_TAIL",
            "FORGE_LOG_STREAM_BUFFER",
            "FORGE_STOP_GRACE_SECONDS",
            "FORGE_ON_CONFIG_CONFLICT",
            "FORGE_NODE_ID",
            "FORGE_NODE_SLOTS",
            "FORGE_NODE_ADDRESS",
            "FORGE_NODE_LABELS",
            "FORGE_NODE_TAINTS",
            "FORGE_NODE_PROVIDER",
            "FORGE_NODE_POOL_ID",
            "FORGE_NODE_CAPACITY_SOURCE",
            "FORGE_ENFORCE_LIMITS",
            "FORGE_NODE_BOOTSTRAP_TOKEN",
            "FORGE_NODE_KEY_DIR",
            "FORGE_HEARTBEAT_INTERVAL_MS",
            "FORGE_DISCOVERY_URL",
            "FORGE_DISCOVERY_REGISTER_ENABLED",
            "FORGE_DISCOVERY_LEASE_SECONDS",
            "FORGE_DISCOVERY_DEFAULT_PROJECT",
            "FORGE_DISCOVERY_DEFAULT_ENVIRONMENT",
            "FORGE_NETWORK_URL",
            "FORGE_NETWORK_NAME",
            "FORGE_NETWORK_WG_BACKEND",
            "FORGE_NETWORK_WG_IFACE",
            "FORGE_NETWORK_WG_ENDPOINT",
            "FORGE_NETWORK_PEER_POLL_INTERVAL_S",
            "FORGE_NODE_DOCKER_COLOCATED",
            "FORGE_NODE_NETWORK_MEMBERSHIP",
            "FORGE_NETWORK_PRIVATE_IFACE",
            "FORGE_NETWORK_ROUTE_BACKEND",
            "FORGE_NETWORK_POLICY_POLL_INTERVAL_S",
            "FORGE_NETWORK_POLICY_BACKEND",
            "FORGE_NETWORK_DENY_LOG_SAMPLE_RATE",
            "FORGE_EVENTS_URL",
            "FORGE_NETWORK_DNS_NAMESERVER",
            "FORGE_NETWORK_DNS_ZONE",
            "FORGE_NETWORK_DNS_SEARCH",
            "FORGE_NETWORK_DNS_BACKEND",
            "FORGE_NETWORK_DNS_RESOLV_PATH",
            "FORGE_NETWORK_OVERLAY_CIDR",
            "FORGE_NETWORK_DRIFT_POLL_INTERVAL_S",
            "FORGE_NETWORK_OVERLAY_REGISTER",
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
                ("FORGE_RECONCILE_INTERVAL_SECONDS", None),
                ("FORGE_CONTROL_REPORT_MODE", None),
                ("FORGE_PULL_TIMEOUT_SECONDS", None),
                ("FORGE_DEFAULT_REGISTRY", None),
                ("FORGE_PROBE_INTERVAL_SECONDS", None),
                ("FORGE_PROBE_TIMEOUT_SECONDS", None),
                ("FORGE_PROBE_FAILURE_THRESHOLD", None),
                ("FORGE_PROBE_READY_PATH", None),
                ("FORGE_PROBE_LIVE_PATH", None),
                ("FORGE_PROBE_HOST", None),
                ("FORGE_LOG_DEFAULT_TAIL", None),
                ("FORGE_LOG_STREAM_BUFFER", None),
                ("FORGE_STOP_GRACE_SECONDS", None),
                ("FORGE_ON_CONFIG_CONFLICT", None),
                ("FORGE_NODE_ID", None),
                ("FORGE_NODE_SLOTS", None),
                ("FORGE_NODE_ADDRESS", None),
                ("FORGE_HEARTBEAT_INTERVAL_MS", None),
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
                assert_eq!(cfg.reconcile_interval, Duration::from_secs(10));
                assert_eq!(cfg.control_report_mode, ReportMode::Push);
                assert_eq!(cfg.pull_timeout, Duration::from_secs(120));
                assert_eq!(cfg.default_registry, "localhost:5000");
                assert_eq!(cfg.probe_interval, Duration::from_secs(5));
                assert_eq!(cfg.probe_timeout, Duration::from_secs(2));
                assert_eq!(cfg.probe_failure_threshold, 3);
                assert_eq!(cfg.probe_ready_path, "/health/ready");
                assert_eq!(cfg.probe_live_path, "/health/live");
                assert_eq!(cfg.probe_host, "127.0.0.1");
                assert_eq!(cfg.log_default_tail, 100);
                assert_eq!(cfg.log_stream_buffer, 8192);
                assert_eq!(cfg.stop_grace, Duration::from_secs(10));
                assert_eq!(
                    cfg.on_config_conflict,
                    crate::lifecycle::OnConfigConflict::Recreate
                );
                assert!(cfg.node_id.is_none());
                assert_eq!(cfg.node_slots, 4);
                assert!(cfg.node_address.is_none());
                assert_eq!(cfg.control_heartbeat_interval, Duration::from_millis(5000));
            },
        );
    }

    #[test]
    fn loads_node_fleet_overrides() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_NODE_ID", Some("node-a")),
                ("FORGE_NODE_SLOTS", Some("8")),
                ("FORGE_NODE_ADDRESS", Some("http://runtime-a:4102")),
                ("FORGE_HEARTBEAT_INTERVAL_MS", Some("2500")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(cfg.node_id.as_deref(), Some("node-a"));
                assert_eq!(cfg.node_slots, 8);
                assert_eq!(cfg.node_address.as_deref(), Some("http://runtime-a:4102"));
                assert_eq!(cfg.control_heartbeat_interval, Duration::from_millis(2500));
            },
        );
    }

    #[test]
    fn rejects_zero_probe_threshold() {
        with_env(
            &[
                ("PORT", Some("8080")),
                ("FORGE_PROBE_FAILURE_THRESHOLD", Some("0")),
            ],
            || {
                assert!(Config::from_env().is_err());
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
                ("FORGE_RECONCILE_INTERVAL_SECONDS", Some("3")),
                ("FORGE_CONTROL_REPORT_MODE", Some("pull")),
            ],
            || {
                let cfg = Config::from_env().expect("config");
                assert_eq!(
                    cfg.control_url.as_deref(),
                    Some("http://forge-control:8080")
                );
                assert_eq!(cfg.data_dir, PathBuf::from("/tmp/forge-runtime-data"));
                assert_eq!(cfg.heartbeat_interval, Duration::from_secs(5));
                assert_eq!(cfg.reconcile_interval, Duration::from_secs(3));
                assert_eq!(cfg.control_report_mode, ReportMode::Pull);
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
