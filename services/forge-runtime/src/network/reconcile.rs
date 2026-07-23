//! Network / Discovery / route drift reconciliation (22.06).
//!
//! Compares Discovery endpoint overlay addresses, Network workload leases, and
//! observed Runtime routes. Drift increments `forge_network_route_drift_total`
//! (reported to forge-network) and is logged with structured fields.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tracing::{info, warn};

use super::dns::{is_overlay_ip, is_provider_public_ip, observe_resolve, DnsObs};
use super::NetworkClient;

/// One Discovery Ready endpoint snapshot for drift comparison.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct EndpointSnapshot {
    pub endpoint_id: String,
    pub address_ip: String,
    pub service: String,
}

/// One Network workload lease.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LeaseSnapshot {
    pub workload_id: String,
    pub address: String,
    pub node_id: String,
}

/// Observed local route for a peer CIDR / overlay IP.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ObservedRoute {
    pub destination: String,
    pub present: bool,
}

/// Drift observability counters (local mirror of forge-network metric).
#[derive(Debug, Default)]
pub struct DriftObs {
    pub route_drift_total: AtomicU64,
}

impl DriftObs {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn bump(&self, n: u64) {
        self.route_drift_total.fetch_add(n, Ordering::Relaxed);
    }

    pub fn total(&self) -> u64 {
        self.route_drift_total.load(Ordering::Relaxed)
    }
}

/// Result of one reconcile pass.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DriftReport {
    pub drifted: Vec<DriftItem>,
    pub public_ip_rejected: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DriftItem {
    pub endpoint_id: String,
    pub expected_overlay_ip: String,
    pub observed_route: String,
}

/// Compare Discovery endpoints, Network leases, and observed routes.
pub fn detect_drift(
    endpoints: &[EndpointSnapshot],
    leases: &[LeaseSnapshot],
    observed: &[ObservedRoute],
    overlay_cidr: &str,
) -> DriftReport {
    let lease_by_id: HashMap<&str, &LeaseSnapshot> = leases
        .iter()
        .map(|l| (l.workload_id.as_str(), l))
        .collect();
    let observed_by_dest: HashMap<&str, &ObservedRoute> = observed
        .iter()
        .map(|o| (o.destination.as_str(), o))
        .collect();

    let mut drifted = Vec::new();
    let mut public_ip_rejected = Vec::new();

    for ep in endpoints {
        if is_provider_public_ip(&ep.address_ip) || !is_overlay_ip(&ep.address_ip, overlay_cidr) {
            public_ip_rejected.push(ep.endpoint_id.clone());
            continue;
        }
        let expected = match lease_by_id.get(ep.endpoint_id.as_str()) {
            Some(l) => l.address.as_str(),
            None => {
                drifted.push(DriftItem {
                    endpoint_id: ep.endpoint_id.clone(),
                    expected_overlay_ip: String::new(),
                    observed_route: format!("discovery={}", ep.address_ip),
                });
                continue;
            }
        };
        if expected != ep.address_ip {
            drifted.push(DriftItem {
                endpoint_id: ep.endpoint_id.clone(),
                expected_overlay_ip: expected.to_string(),
                observed_route: format!("discovery={}", ep.address_ip),
            });
            continue;
        }
        let route_ok = observed_by_dest
            .get(expected)
            .map(|o| o.present)
            .or_else(|| {
                // CIDR form: 10.100.2.5 covered by 10.100.2.0/24
                observed.iter().find_map(|o| {
                    if route_covers(&o.destination, expected) && o.present {
                        Some(true)
                    } else {
                        None
                    }
                })
            })
            .unwrap_or(false);
        if !route_ok {
            drifted.push(DriftItem {
                endpoint_id: ep.endpoint_id.clone(),
                expected_overlay_ip: expected.to_string(),
                observed_route: "missing".into(),
            });
        }
    }
    DriftReport {
        drifted,
        public_ip_rejected,
    }
}

fn route_covers(cidr_or_ip: &str, ip: &str) -> bool {
    if cidr_or_ip == ip {
        return true;
    }
    let Some((net_s, prefix_s)) = cidr_or_ip.split_once('/') else {
        return false;
    };
    let Ok(net) = net_s.parse::<std::net::Ipv4Addr>() else {
        return false;
    };
    let Ok(addr) = ip.parse::<std::net::Ipv4Addr>() else {
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

/// Log drift items with the contract fields.
pub fn log_drift(items: &[DriftItem]) {
    for d in items {
        warn!(
            event = "network.route.drift",
            endpoint_id = %d.endpoint_id,
            expected_overlay_ip = %d.expected_overlay_ip,
            observed_route = %d.observed_route,
            "discovery/network/route drift detected"
        );
    }
}

/// Config for the Runtime drift poll loop.
#[derive(Clone)]
pub struct DriftPollConfig {
    pub network_url: String,
    pub network_name: String,
    #[allow(dead_code)]
    pub node_id: String,
    pub discovery_url: Option<String>,
    pub overlay_cidr: String,
    pub poll_interval: Duration,
    pub dns_obs: Arc<DnsObs>,
    pub drift_obs: Arc<DriftObs>,
    /// Local observed routes (shared with route backend / tests).
    pub observed_routes: Arc<std::sync::Mutex<Vec<ObservedRoute>>>,
}

/// Report drift count to forge-network.
impl NetworkClient {
    pub async fn list_workload_leases(
        &self,
        network: &str,
    ) -> Result<Vec<LeaseSnapshot>, String> {
        let url = format!(
            "{}/v1/networks/{}/workload-leases",
            self.base_url,
            super::urlencoding_lite(network)
        );
        let resp = self
            .http
            .get(&url)
            .send()
            .await
            .map_err(|e| format!("list leases: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("list leases HTTP {status}: {body}"));
        }
        #[derive(Deserialize)]
        struct Wrap {
            leases: Vec<LeaseSnapshot>,
        }
        let wrap = resp
            .json::<Wrap>()
            .await
            .map_err(|e| format!("decode leases: {e}"))?;
        Ok(wrap.leases)
    }

    pub async fn allocate_workload_lease(
        &self,
        network: &str,
        node_id: &str,
        workload_id: &str,
    ) -> Result<LeaseSnapshot, String> {
        #[derive(Serialize)]
        struct Body<'a> {
            node_id: &'a str,
            workload_id: &'a str,
        }
        let url = format!(
            "{}/v1/networks/{}/workload-leases",
            self.base_url,
            super::urlencoding_lite(network)
        );
        let resp = self
            .http
            .post(&url)
            .json(&Body {
                node_id,
                workload_id,
            })
            .send()
            .await
            .map_err(|e| format!("allocate lease: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("allocate lease HTTP {status}: {body}"));
        }
        #[derive(Deserialize)]
        struct Resp {
            workload_id: String,
            address: String,
        }
        let r = resp
            .json::<Resp>()
            .await
            .map_err(|e| format!("decode lease: {e}"))?;
        Ok(LeaseSnapshot {
            workload_id: r.workload_id,
            address: r.address,
            node_id: node_id.to_string(),
        })
    }

    pub async fn report_route_drift(
        &self,
        network: &str,
        count: u64,
    ) -> Result<(), String> {
        #[derive(Serialize)]
        struct Body {
            drift_count: u64,
        }
        let url = format!(
            "{}/v1/networks/{}/route-drift",
            self.base_url,
            super::urlencoding_lite(network)
        );
        let resp = self
            .http
            .post(&url)
            .json(&Body { drift_count: count })
            .send()
            .await
            .map_err(|e| format!("report drift: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("report drift HTTP {status}: {body}"));
        }
        Ok(())
    }
}

async fn fetch_ready_endpoints(
    discovery_url: &str,
) -> Result<Vec<EndpointSnapshot>, String> {
    let base = discovery_url.trim().trim_end_matches('/');
    let http = reqwest::Client::builder()
        .timeout(Duration::from_secs(5))
        .build()
        .map_err(|e| format!("discovery client: {e}"))?;
    let services_url = format!("{base}/v1/services");
    let resp = http
        .get(&services_url)
        .send()
        .await
        .map_err(|e| format!("list services: {e}"))?;
    if !resp.status().is_success() {
        return Err(format!("list services HTTP {}", resp.status()));
    }
    #[derive(Deserialize)]
    struct Svc {
        project: String,
        environment: String,
        name: String,
    }
    #[derive(Deserialize)]
    struct SvcWrap {
        #[serde(default)]
        items: Vec<Svc>,
        #[serde(default)]
        services: Vec<Svc>,
    }
    let bytes = resp
        .bytes()
        .await
        .map_err(|e| format!("read services: {e}"))?;
    let services = if let Ok(list) = serde_json::from_slice::<Vec<Svc>>(&bytes) {
        list
    } else {
        let wrap: SvcWrap =
            serde_json::from_slice(&bytes).map_err(|e| format!("decode services: {e}"))?;
        if wrap.items.is_empty() {
            wrap.services
        } else {
            wrap.items
        }
    };

    let mut out = Vec::new();
    for svc in services {
        let url = format!(
            "{base}/v1/projects/{}/environments/{}/services/{}/endpoints",
            enc(&svc.project),
            enc(&svc.environment),
            enc(&svc.name)
        );
        let resp = http
            .get(&url)
            .send()
            .await
            .map_err(|e| format!("list endpoints: {e}"))?;
        if !resp.status().is_success() {
            continue;
        }
        #[derive(Deserialize)]
        struct Ep {
            id: String,
            #[serde(default)]
            address: Option<EpAddr>,
            #[serde(default)]
            address_ip: Option<String>,
            #[serde(default)]
            phase: Option<String>,
        }
        #[derive(Deserialize)]
        struct EpAddr {
            ip: String,
        }
        #[derive(Deserialize)]
        struct EpWrap {
            #[serde(default)]
            items: Vec<Ep>,
            #[serde(default)]
            endpoints: Vec<Ep>,
        }
        let bytes = match resp.bytes().await {
            Ok(b) => b,
            Err(_) => continue,
        };
        let eps = if let Ok(list) = serde_json::from_slice::<Vec<Ep>>(&bytes) {
            list
        } else {
            match serde_json::from_slice::<EpWrap>(&bytes) {
                Ok(wrap) if !wrap.items.is_empty() => wrap.items,
                Ok(wrap) => wrap.endpoints,
                Err(_) => continue,
            }
        };
        for ep in eps {
            if ep.phase.as_deref().unwrap_or("Ready") != "Ready" {
                continue;
            }
            let ip = ep
                .address
                .map(|a| a.ip)
                .or(ep.address_ip)
                .unwrap_or_default();
            if ip.is_empty() {
                continue;
            }
            out.push(EndpointSnapshot {
                endpoint_id: ep.id,
                address_ip: ip,
                service: svc.name.clone(),
            });
        }
    }
    Ok(out)
}

fn enc(s: &str) -> String {
    super::urlencoding_lite(s)
}

/// One drift poll cycle.
pub async fn drift_poll_once(client: &NetworkClient, cfg: &DriftPollConfig) {
    let leases = match client.list_workload_leases(&cfg.network_name).await {
        Ok(l) => l,
        Err(err) => {
            warn!(error = %err, "drift: list leases failed");
            return;
        }
    };
    let endpoints = if let Some(url) = cfg.discovery_url.as_deref() {
        match fetch_ready_endpoints(url).await {
            Ok(e) => e,
            Err(err) => {
                warn!(error = %err, "drift: list discovery endpoints failed");
                Vec::new()
            }
        }
    } else {
        Vec::new()
    };
    let observed = cfg
        .observed_routes
        .lock()
        .unwrap()
        .clone();
    let report = detect_drift(&endpoints, &leases, &observed, &cfg.overlay_cidr);
    if !report.drifted.is_empty() {
        log_drift(&report.drifted);
        cfg.drift_obs.bump(report.drifted.len() as u64);
        if let Err(err) = client
            .report_route_drift(&cfg.network_name, report.drifted.len() as u64)
            .await
        {
            warn!(error = %err, "drift: report failed");
        }
    }
    for ep in &endpoints {
        let name = format!("{}.svc.forge", ep.service);
        if is_overlay_ip(&ep.address_ip, &cfg.overlay_cidr)
            && !is_provider_public_ip(&ep.address_ip)
        {
            observe_resolve(
                &cfg.dns_obs,
                &name,
                "ok",
                Some(&ep.address_ip),
            );
        } else {
            observe_resolve(&cfg.dns_obs, &name, "error", Some(&ep.address_ip));
        }
    }
    if report.drifted.is_empty() && !endpoints.is_empty() {
        info!(
            event = "network.route.drift_ok",
            endpoints = endpoints.len(),
            leases = leases.len(),
            "no discovery/network/route drift"
        );
    }
}

pub fn spawn_drift_poll_loop(cfg: DriftPollConfig) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let client = match NetworkClient::new(&cfg.network_url) {
            Ok(c) => c,
            Err(err) => {
                warn!(error = %err, "drift poll loop disabled");
                return;
            }
        };
        loop {
            drift_poll_once(&client, &cfg).await;
            sleep(cfg.poll_interval).await;
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use httpmock::prelude::*;

    #[test]
    fn detects_missing_lease_and_public_ip() {
        let endpoints = vec![
            EndpointSnapshot {
                endpoint_id: "echo-1".into(),
                address_ip: "10.100.2.5".into(),
                service: "echo".into(),
            },
            EndpointSnapshot {
                endpoint_id: "bad-1".into(),
                address_ip: "203.0.113.9".into(),
                service: "bad".into(),
            },
        ];
        let leases = vec![LeaseSnapshot {
            workload_id: "echo-1".into(),
            address: "10.100.2.5".into(),
            node_id: "node-b".into(),
        }];
        let observed = vec![ObservedRoute {
            destination: "10.100.2.0/24".into(),
            present: true,
        }];
        let report = detect_drift(&endpoints, &leases, &observed, "10.100.0.0/16");
        assert!(report.drifted.is_empty());
        assert_eq!(report.public_ip_rejected, vec!["bad-1".to_string()]);
    }

    #[test]
    fn detects_address_mismatch_drift() {
        let endpoints = vec![EndpointSnapshot {
            endpoint_id: "echo-1".into(),
            address_ip: "10.100.2.9".into(),
            service: "echo".into(),
        }];
        let leases = vec![LeaseSnapshot {
            workload_id: "echo-1".into(),
            address: "10.100.2.5".into(),
            node_id: "node-b".into(),
        }];
        let report = detect_drift(&endpoints, &leases, &[], "10.100.0.0/16");
        assert_eq!(report.drifted.len(), 1);
        assert_eq!(report.drifted[0].expected_overlay_ip, "10.100.2.5");
        assert_eq!(report.drifted[0].observed_route, "discovery=10.100.2.9");
    }

    #[tokio::test]
    async fn allocate_and_list_leases_via_client() {
        let server = MockServer::start();
        let _alloc = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/networks/cluster-overlay/workload-leases");
            then.status(200).json_body(serde_json::json!({
                "workload_id": "wl-1",
                "address": "10.100.1.5"
            }));
        });
        let _list = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/networks/cluster-overlay/workload-leases");
            then.status(200).json_body(serde_json::json!({
                "leases": [{
                    "workload_id": "wl-1",
                    "address": "10.100.1.5",
                    "node_id": "node-a"
                }]
            }));
        });
        let client = NetworkClient::new(&server.base_url()).unwrap();
        let lease = client
            .allocate_workload_lease("cluster-overlay", "node-a", "wl-1")
            .await
            .unwrap();
        assert_eq!(lease.address, "10.100.1.5");
        let list = client
            .list_workload_leases("cluster-overlay")
            .await
            .unwrap();
        assert_eq!(list.len(), 1);
    }
}
