//! Forge DNS bootstrap for `.svc.forge` (22.06).
//!
//! Runtime configures the node resolver so workloads use Discovery's overlay
//! nameserver for the internal zone. On DNS apply failure the previous config
//! is kept and the node is marked `Degraded`.

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use tracing::{info, warn};

/// Desired Forge DNS resolver settings.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DnsConfig {
    pub nameserver: String,
    pub zone: String,
    pub search: String,
}

impl DnsConfig {
    pub fn render_resolv(&self) -> String {
        format!(
            "# forge-runtime managed (22.06)\nnameserver {}\nsearch {}\noptions ndots:1\n# zone {}\n",
            self.nameserver.trim(),
            self.search.trim(),
            self.zone.trim()
        )
    }
}

/// Applies / reads DNS resolver configuration.
pub trait DnsConfigBackend: Send + Sync {
    fn apply(&self, cfg: &DnsConfig) -> Result<(), String>;
    fn current(&self) -> Option<DnsConfig>;
}

/// In-memory backend for unit tests / CI.
#[derive(Debug, Default)]
pub struct FakeDnsBackend {
    state: Mutex<FakeDnsState>,
}

#[derive(Debug, Default)]
struct FakeDnsState {
    current: Option<DnsConfig>,
    apply_failures: u32,
    apply_count: u32,
}

impl FakeDnsBackend {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(FakeDnsState::default()),
        }
    }

    pub fn force_fail_next(&self, n: u32) {
        self.state.lock().unwrap().apply_failures = n;
    }

    #[allow(dead_code)]
    pub fn apply_count(&self) -> u32 {
        self.state.lock().unwrap().apply_count
    }
}

impl DnsConfigBackend for FakeDnsBackend {
    fn apply(&self, cfg: &DnsConfig) -> Result<(), String> {
        let mut st = self.state.lock().unwrap();
        if st.apply_failures > 0 {
            st.apply_failures -= 1;
            return Err("forced dns apply failure".into());
        }
        st.current = Some(cfg.clone());
        st.apply_count = st.apply_count.saturating_add(1);
        Ok(())
    }

    fn current(&self) -> Option<DnsConfig> {
        self.state.lock().unwrap().current.clone()
    }
}

/// Host backend writing a forge-managed resolv snippet (never overwrites system resolv.conf
/// blindly — writes `FORGE_NETWORK_DNS_RESOLV_PATH`, default under data dir).
pub struct HostDnsBackend {
    path: PathBuf,
    last: Mutex<Option<DnsConfig>>,
}

impl HostDnsBackend {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self {
            path: path.into(),
            last: Mutex::new(None),
        }
    }
}

impl DnsConfigBackend for HostDnsBackend {
    fn apply(&self, cfg: &DnsConfig) -> Result<(), String> {
        if let Some(parent) = self.path.parent() {
            fs::create_dir_all(parent).map_err(|e| format!("dns mkdir: {e}"))?;
        }
        let content = cfg.render_resolv();
        let tmp = self.path.with_extension("tmp");
        fs::write(&tmp, &content).map_err(|e| format!("dns write tmp: {e}"))?;
        fs::rename(&tmp, &self.path).map_err(|e| format!("dns rename: {e}"))?;
        *self.last.lock().unwrap() = Some(cfg.clone());
        Ok(())
    }

    fn current(&self) -> Option<DnsConfig> {
        if let Some(c) = self.last.lock().unwrap().clone() {
            return Some(c);
        }
        read_resolv_file(&self.path).ok()
    }
}

fn read_resolv_file(path: &Path) -> Result<DnsConfig, String> {
    let raw = fs::read_to_string(path).map_err(|e| format!("dns read: {e}"))?;
    let mut nameserver = String::new();
    let mut search = String::new();
    let mut zone = "svc.forge".to_string();
    for line in raw.lines() {
        let line = line.trim();
        if let Some(rest) = line.strip_prefix("nameserver ") {
            nameserver = rest.trim().to_string();
        } else if let Some(rest) = line.strip_prefix("search ") {
            search = rest.trim().to_string();
        } else if let Some(rest) = line.strip_prefix("# zone ") {
            zone = rest.trim().to_string();
        }
    }
    if nameserver.is_empty() {
        return Err("no nameserver in resolv file".into());
    }
    if search.is_empty() {
        search = zone.clone();
    }
    Ok(DnsConfig {
        nameserver,
        zone,
        search,
    })
}

/// Observability for DNS resolution / bootstrap.
#[derive(Debug, Default)]
pub struct DnsObs {
    pub resolution_ok: AtomicU64,
    pub resolution_error: AtomicU64,
    pub resolution_nxdomain: AtomicU64,
}

impl DnsObs {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn record(&self, result: &str) {
        match result {
            "ok" => {
                self.resolution_ok.fetch_add(1, Ordering::Relaxed);
            }
            "nxdomain" => {
                self.resolution_nxdomain.fetch_add(1, Ordering::Relaxed);
            }
            _ => {
                self.resolution_error.fetch_add(1, Ordering::Relaxed);
            }
        }
    }

    pub fn total_ok(&self) -> u64 {
        self.resolution_ok.load(Ordering::Relaxed)
    }

    #[allow(dead_code)]
    pub fn total_error(&self) -> u64 {
        self.resolution_error.load(Ordering::Relaxed)
    }
}

/// Shared node health for DNS degradation (22.06).
#[derive(Debug, Default)]
pub struct NodeNetworkHealth {
    dns_degraded: Mutex<bool>,
}

impl NodeNetworkHealth {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn set_dns_degraded(&self, degraded: bool) {
        *self.dns_degraded.lock().unwrap() = degraded;
    }

    pub fn dns_degraded(&self) -> bool {
        *self.dns_degraded.lock().unwrap()
    }

    /// `Ready` when DNS is healthy; `Degraded` when DNS apply failed and last-good kept.
    pub fn status(&self) -> &'static str {
        if self.dns_degraded() {
            "Degraded"
        } else {
            "Ready"
        }
    }
}

/// Apply Forge DNS config. On failure keep previous config and mark Degraded.
pub fn bootstrap_dns(
    backend: &dyn DnsConfigBackend,
    desired: &DnsConfig,
    health: &NodeNetworkHealth,
) -> Result<(), String> {
    match backend.apply(desired) {
        Ok(()) => {
            health.set_dns_degraded(false);
            info!(
                event = "network.dns.bootstrap",
                nameserver = %desired.nameserver,
                zone = %desired.zone,
                search = %desired.search,
                "applied forge dns resolver config"
            );
            Ok(())
        }
        Err(err) => {
            health.set_dns_degraded(true);
            warn!(
                event = "network.dns.bootstrap_failed",
                error = %err,
                status = "Degraded",
                "dns apply failed; keeping existing resolver config"
            );
            Err(err)
        }
    }
}

/// Record a synthetic resolve observation (used by reconcile / tests) under span name
/// `network.dns.resolve`.
pub fn observe_resolve(obs: &DnsObs, name: &str, result: &str, overlay_ip: Option<&str>) {
    let _span = tracing::info_span!(
        "network.dns.resolve",
        name = %name,
        result = %result,
        overlay_ip = overlay_ip.unwrap_or(""),
    )
    .entered();
    obs.record(result);
    match result {
        "ok" => info!(
            event = "network.dns.resolve",
            name = %name,
            result = "ok",
            overlay_ip = overlay_ip.unwrap_or(""),
            "dns resolve ok"
        ),
        other => warn!(
            event = "network.dns.resolve",
            name = %name,
            result = %other,
            "dns resolve failed"
        ),
    }
}

pub fn select_dns_backend(kind: &str, resolv_path: &Path) -> Arc<dyn DnsConfigBackend> {
    match kind.trim().to_ascii_lowercase().as_str() {
        "fake" => Arc::new(FakeDnsBackend::new()),
        _ => Arc::new(HostDnsBackend::new(resolv_path.to_path_buf())),
    }
}

/// True when an address is a public (non-RFC1918 / non-link-local / non-loopback) IPv4.
pub fn is_provider_public_ip(ip: &str) -> bool {
    let Ok(addr) = ip.parse::<std::net::Ipv4Addr>() else {
        // IPv6 / unparseable: treat non-overlay conservatively as public for DNS filter helpers.
        return !ip.starts_with("10.") && !ip.starts_with("fd");
    };
    let octets = addr.octets();
    // RFC1918
    if octets[0] == 10 {
        return false;
    }
    if octets[0] == 172 && (16..=31).contains(&octets[1]) {
        return false;
    }
    if octets[0] == 192 && octets[1] == 168 {
        return false;
    }
    // loopback / link-local / CGNAT
    if octets[0] == 127 || (octets[0] == 169 && octets[1] == 254) {
        return false;
    }
    if octets[0] == 100 && (64..=127).contains(&octets[1]) {
        return false;
    }
    true
}

/// True when IPv4 is inside the overlay CIDR (default `10.100.0.0/16`).
pub fn is_overlay_ip(ip: &str, overlay_cidr: &str) -> bool {
    let Ok(addr) = ip.parse::<std::net::Ipv4Addr>() else {
        return false;
    };
    let Some((net_s, prefix_s)) = overlay_cidr.split_once('/') else {
        return false;
    };
    let Ok(net) = net_s.parse::<std::net::Ipv4Addr>() else {
        return false;
    };
    let Ok(prefix) = prefix_s.parse::<u32>() else {
        return false;
    };
    if prefix > 32 {
        return false;
    }
    let mask = if prefix == 0 {
        0u32
    } else {
        u32::MAX << (32 - prefix)
    };
    (u32::from(addr) & mask) == (u32::from(net) & mask)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn bootstrap_applies_and_clears_degraded() {
        let backend = FakeDnsBackend::new();
        let health = NodeNetworkHealth::new();
        let cfg = DnsConfig {
            nameserver: "10.100.0.53".into(),
            zone: "svc.forge".into(),
            search: "production.shop.svc.forge".into(),
        };
        bootstrap_dns(&backend, &cfg, &health).unwrap();
        assert_eq!(health.status(), "Ready");
        assert_eq!(backend.current().as_ref(), Some(&cfg));
    }

    #[test]
    fn bootstrap_failure_marks_degraded_keeps_previous() {
        let backend = FakeDnsBackend::new();
        let health = NodeNetworkHealth::new();
        let good = DnsConfig {
            nameserver: "10.100.0.53".into(),
            zone: "svc.forge".into(),
            search: "local.demo.svc.forge".into(),
        };
        bootstrap_dns(&backend, &good, &health).unwrap();
        backend.force_fail_next(1);
        let bad = DnsConfig {
            nameserver: "10.100.0.99".into(),
            zone: "svc.forge".into(),
            search: "local.demo.svc.forge".into(),
        };
        assert!(bootstrap_dns(&backend, &bad, &health).is_err());
        assert_eq!(health.status(), "Degraded");
        assert_eq!(backend.current().as_ref(), Some(&good));
    }

    #[test]
    fn host_backend_roundtrip() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("resolv.forge.conf");
        let backend = HostDnsBackend::new(&path);
        let cfg = DnsConfig {
            nameserver: "172.30.0.53".into(),
            zone: "svc.forge".into(),
            search: "local.demo.svc.forge".into(),
        };
        backend.apply(&cfg).unwrap();
        assert_eq!(backend.current().unwrap(), cfg);
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("nameserver 172.30.0.53"));
        assert!(raw.contains("search local.demo.svc.forge"));
    }

    #[test]
    fn public_ip_detection() {
        assert!(is_provider_public_ip("203.0.113.10"));
        assert!(!is_provider_public_ip("10.100.1.5"));
        assert!(!is_provider_public_ip("192.168.1.1"));
        assert!(!is_provider_public_ip("127.0.0.1"));
    }

    #[test]
    fn observe_resolve_increments_metric() {
        let obs = DnsObs::new();
        observe_resolve(&obs, "echo.local.demo.svc.forge", "ok", Some("10.100.2.5"));
        observe_resolve(&obs, "missing.local.demo.svc.forge", "nxdomain", None);
        assert_eq!(obs.total_ok(), 1);
        assert_eq!(obs.resolution_nxdomain.load(Ordering::Relaxed), 1);
    }
}
