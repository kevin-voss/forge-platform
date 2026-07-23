//! Per-pair transport application (22.04).
//!
//! * `docker` — ensure a per-node Docker user-defined network with the leased subnet;
//!   never starts a WireGuard interface for that pair.
//! * `provider-private` — add a host route for the peer `/24` via the private NIC;
//!   no tunnel.
//! * `wireguard` — hand off to [`super::wireguard`].

use serde::Deserialize;
use std::collections::HashSet;
use std::process::Command;
use std::sync::{Arc, Mutex};
use tracing::info;

/// Transport mode for one directed pair.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Transport {
    Docker,
    #[serde(rename = "provider-private")]
    ProviderPrivate,
    Wireguard,
}

impl Transport {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim() {
            "docker" => Ok(Self::Docker),
            "provider-private" => Ok(Self::ProviderPrivate),
            "wireguard" => Ok(Self::Wireguard),
            other => Err(format!("unknown transport {other:?}")),
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct TransportPair {
    #[allow(dead_code)]
    pub from: String,
    #[allow(dead_code)]
    pub to: String,
    pub transport: String,
}

/// One peer route to realize locally.
#[derive(Debug, Clone)]
pub struct PeerRoute {
    #[allow(dead_code)]
    pub peer_node_id: String,
    pub peer_cidr: String,
    pub transport: Transport,
}

/// Applies non-WireGuard transports (docker networks + provider routes).
pub trait RouteBackend: Send + Sync {
    fn ensure_docker_network(&self, name: &str, subnet: &str) -> Result<(), String>;
    fn add_provider_route(&self, cidr: &str, via_iface: &str) -> Result<(), String>;
    /// True if a WireGuard interface was brought up (used by tests / observability).
    fn wireguard_iface_started(&self) -> bool {
        false
    }
}

/// In-memory backend for CI / unit tests.
#[derive(Debug, Default)]
pub struct FakeRouteBackend {
    state: Mutex<FakeRouteState>,
}

#[derive(Debug, Default)]
struct FakeRouteState {
    docker_networks: HashSet<(String, String)>,
    provider_routes: HashSet<(String, String)>,
    wg_started: bool,
}

impl FakeRouteBackend {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(FakeRouteState::default()),
        }
    }

    pub fn docker_network_count(&self) -> usize {
        self.state.lock().unwrap().docker_networks.len()
    }

    pub fn provider_route_count(&self) -> usize {
        self.state.lock().unwrap().provider_routes.len()
    }
}

impl RouteBackend for FakeRouteBackend {
    fn ensure_docker_network(&self, name: &str, subnet: &str) -> Result<(), String> {
        self.state
            .lock()
            .unwrap()
            .docker_networks
            .insert((name.to_string(), subnet.to_string()));
        Ok(())
    }

    fn add_provider_route(&self, cidr: &str, via_iface: &str) -> Result<(), String> {
        self.state
            .lock()
            .unwrap()
            .provider_routes
            .insert((cidr.to_string(), via_iface.to_string()));
        Ok(())
    }

    fn wireguard_iface_started(&self) -> bool {
        self.state.lock().unwrap().wg_started
    }
}

/// Host backend: Docker Engine API via `docker network create`, `ip route` for provider.
pub struct HostRouteBackend;

impl RouteBackend for HostRouteBackend {
    fn ensure_docker_network(&self, name: &str, subnet: &str) -> Result<(), String> {
        // Idempotent: inspect first; create when missing.
        let inspect = Command::new("docker")
            .args(["network", "inspect", name])
            .output()
            .map_err(|e| format!("docker network inspect: {e}"))?;
        if inspect.status.success() {
            return Ok(());
        }
        let out = Command::new("docker")
            .args([
                "network",
                "create",
                "--driver",
                "bridge",
                "--subnet",
                subnet,
                name,
            ])
            .output()
            .map_err(|e| format!("docker network create: {e}"))?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            if stderr.contains("already exists") {
                return Ok(());
            }
            return Err(format!("docker network create failed: {stderr}"));
        }
        info!(network = %name, subnet = %subnet, "ensured docker overlay network");
        Ok(())
    }

    fn add_provider_route(&self, cidr: &str, via_iface: &str) -> Result<(), String> {
        // `ip route replace` is idempotent.
        let out = Command::new("ip")
            .args(["route", "replace", cidr, "dev", via_iface])
            .output()
            .map_err(|e| format!("ip route replace: {e}"))?;
        if !out.status.success() {
            return Err(format!(
                "ip route replace failed: {}",
                String::from_utf8_lossy(&out.stderr)
            ));
        }
        info!(cidr = %cidr, iface = %via_iface, "added provider-private route");
        Ok(())
    }
}

/// Apply non-WG routes; returns whether any peer still needs WireGuard.
pub fn apply_routes(
    backend: &dyn RouteBackend,
    local_node: &str,
    local_cidr: Option<&str>,
    private_iface: &str,
    routes: &[PeerRoute],
) -> Result<bool, String> {
    let mut needs_wg = false;
    for r in routes {
        match r.transport {
            Transport::Docker => {
                if let Some(cidr) = local_cidr {
                    let net_name = format!("forge-overlay-{local_node}");
                    backend.ensure_docker_network(&net_name, cidr)?;
                }
            }
            Transport::ProviderPrivate => {
                backend.add_provider_route(&r.peer_cidr, private_iface)?;
            }
            Transport::Wireguard => {
                needs_wg = true;
            }
        }
    }
    Ok(needs_wg)
}

/// Select route backend: fake when `FORGE_NETWORK_ROUTE_BACKEND=fake`, else host.
pub fn select_route_backend(kind: &str) -> Arc<dyn RouteBackend> {
    match kind.trim().to_ascii_lowercase().as_str() {
        "fake" | "mock" => Arc::new(FakeRouteBackend::new()),
        _ => Arc::new(HostRouteBackend),
    }
}

/// Decide whether WireGuard should be started for a peer set size.
/// Docker / provider-private-only meshes never bring up `wg0`.
pub fn should_start_wireguard(needs_wg_peers: bool) -> bool {
    needs_wg_peers
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn docker_routes_never_need_wireguard() {
        let backend = FakeRouteBackend::new();
        let routes = vec![PeerRoute {
            peer_node_id: "node-b".into(),
            peer_cidr: "10.100.2.0/24".into(),
            transport: Transport::Docker,
        }];
        let needs_wg = apply_routes(&backend, "node-a", Some("10.100.1.0/24"), "eth1", &routes)
            .unwrap();
        assert!(!needs_wg);
        assert!(!should_start_wireguard(needs_wg));
        assert_eq!(backend.docker_network_count(), 1);
        assert!(!backend.wireguard_iface_started());
    }

    #[test]
    fn provider_private_adds_route_no_tunnel() {
        let backend = FakeRouteBackend::new();
        let routes = vec![PeerRoute {
            peer_node_id: "node-b".into(),
            peer_cidr: "10.100.2.0/24".into(),
            transport: Transport::ProviderPrivate,
        }];
        let needs_wg = apply_routes(&backend, "node-a", None, "eth1", &routes).unwrap();
        assert!(!needs_wg);
        assert_eq!(backend.provider_route_count(), 1);
    }

    #[test]
    fn mixed_cluster_marks_wireguard_needed() {
        let backend = FakeRouteBackend::new();
        let routes = vec![
            PeerRoute {
                peer_node_id: "node-b".into(),
                peer_cidr: "10.100.2.0/24".into(),
                transport: Transport::ProviderPrivate,
            },
            PeerRoute {
                peer_node_id: "node-c".into(),
                peer_cidr: "10.100.3.0/24".into(),
                transport: Transport::Wireguard,
            },
        ];
        let needs_wg = apply_routes(&backend, "node-a", None, "eth1", &routes).unwrap();
        assert!(needs_wg);
        assert_eq!(backend.provider_route_count(), 1);
    }

    #[test]
    fn transport_parse() {
        assert_eq!(Transport::parse("docker").unwrap(), Transport::Docker);
        assert_eq!(
            Transport::parse("provider-private").unwrap(),
            Transport::ProviderPrivate
        );
        assert!(Transport::parse("nope").is_err());
    }
}
