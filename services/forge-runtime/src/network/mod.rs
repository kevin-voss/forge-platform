//! Runtime network module: WireGuard peer poll / apply / report (22.03),
//! per-pair transport routes (22.04), NetworkPolicy enforcement (22.05),
//! and overlay DNS / Discovery drift (22.06).

mod dns;
mod policy;
mod reconcile;
mod route;
mod wireguard;

#[cfg(test)]
mod cross_node_test;

pub use dns::{
    bootstrap_dns, is_overlay_ip, is_provider_public_ip, select_dns_backend, DnsConfig, DnsObs,
    NodeNetworkHealth,
};
pub use policy::{
    select_policy_backend, spawn_policy_poll_loop, PolicyObs, PolicyPollConfig,
};
pub use reconcile::{spawn_drift_poll_loop, DriftObs, DriftPollConfig};
pub use route::{
    apply_routes, select_route_backend, should_start_wireguard, FakeRouteBackend, PeerRoute,
    RouteBackend, Transport, TransportPair,
};
pub use wireguard::{select_backend, FakeWgBackend, PeerSet, WgBackend, WgBackendKind};

use serde::Serialize;
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tracing::{debug, info, warn};

/// Configuration for the peer poll loop.
#[derive(Clone)]
pub struct PeerPollConfig {
    pub network_url: String,
    pub network_name: String,
    pub node_id: String,
    pub public_key: String,
    pub endpoint: Option<String>,
    pub iface: String,
    pub poll_interval: Duration,
    pub backend: Arc<dyn WgBackend>,
    pub route_backend: Arc<dyn RouteBackend>,
    pub docker_colocated: bool,
    pub network_membership: Option<String>,
    pub private_iface: String,
    pub local_cidr: Option<String>,
}

/// HTTP client against forge-network peer APIs.
#[derive(Clone)]
pub struct NetworkClient {
    pub(crate) base_url: String,
    pub(crate) http: reqwest::Client,
}

impl NetworkClient {
    pub fn new(base_url: &str) -> Result<Self, String> {
        let base = base_url.trim().trim_end_matches('/').to_string();
        if base.is_empty() {
            return Err("FORGE_NETWORK_URL is empty".into());
        }
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .map_err(|e| format!("network client: {e}"))?;
        Ok(Self {
            base_url: base,
            http,
        })
    }

    pub async fn register_peer(
        &self,
        network: &str,
        node_id: &str,
        public_key: &str,
        endpoint: Option<&str>,
    ) -> Result<(), String> {
        #[derive(Serialize)]
        struct Body<'a> {
            public_key: &'a str,
            #[serde(skip_serializing_if = "Option::is_none")]
            endpoint: Option<&'a str>,
        }
        let url = format!(
            "{}/v1/networks/{}/nodes/{}/wireguard",
            self.base_url,
            enc(network),
            enc(node_id)
        );
        let resp = self
            .http
            .put(&url)
            .json(&Body {
                public_key,
                endpoint,
            })
            .send()
            .await
            .map_err(|e| format!("register peer: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("register peer HTTP {status}: {body}"));
        }
        Ok(())
    }

    pub async fn fetch_peers(&self, network: &str, node_id: &str) -> Result<PeerSet, String> {
        let url = format!(
            "{}/v1/networks/{}/nodes/{}/peers",
            self.base_url,
            enc(network),
            enc(node_id)
        );
        let resp = self
            .http
            .get(&url)
            .send()
            .await
            .map_err(|e| format!("fetch peers: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("fetch peers HTTP {status}: {body}"));
        }
        resp.json::<PeerSet>()
            .await
            .map_err(|e| format!("decode peers: {e}"))
    }

    pub async fn report_applied(
        &self,
        network: &str,
        node_id: &str,
        version: i64,
    ) -> Result<(), String> {
        #[derive(Serialize)]
        struct Body {
            applied_peer_version: i64,
        }
        let url = format!(
            "{}/v1/networks/{}/nodes/{}/applied-version",
            self.base_url,
            enc(network),
            enc(node_id)
        );
        let resp = self
            .http
            .post(&url)
            .json(&Body {
                applied_peer_version: version,
            })
            .send()
            .await
            .map_err(|e| format!("report applied: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("report applied HTTP {status}: {body}"));
        }
        Ok(())
    }

    pub async fn patch_membership(
        &self,
        node_id: &str,
        membership: Option<&str>,
        docker_colocated: Option<bool>,
    ) -> Result<(), String> {
        #[derive(Serialize)]
        struct Body<'a> {
            #[serde(skip_serializing_if = "Option::is_none")]
            membership: Option<&'a str>,
            #[serde(skip_serializing_if = "Option::is_none")]
            docker_colocated: Option<bool>,
        }
        let url = format!(
            "{}/v1/nodes/{}/network-membership",
            self.base_url,
            enc(node_id)
        );
        let resp = self
            .http
            .patch(&url)
            .json(&Body {
                membership,
                docker_colocated,
            })
            .send()
            .await
            .map_err(|e| format!("patch membership: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("patch membership HTTP {status}: {body}"));
        }
        Ok(())
    }

    pub async fn fetch_transport(
        &self,
        network: &str,
        from: &str,
        to: &str,
    ) -> Result<TransportPair, String> {
        let url = format!(
            "{}/v1/networks/{}/transport?from={}&to={}",
            self.base_url,
            enc(network),
            enc(from),
            enc(to)
        );
        let resp = self
            .http
            .get(&url)
            .send()
            .await
            .map_err(|e| format!("fetch transport: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("fetch transport HTTP {status}: {body}"));
        }
        resp.json::<TransportPair>()
            .await
            .map_err(|e| format!("decode transport: {e}"))
    }
}

fn enc(s: &str) -> String {
    urlencoding_lite(s)
}

pub(crate) fn urlencoding_lite(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// Split the distributed peer set by per-pair transport and apply non-WG routes.
/// Returns the peer subset that still needs WireGuard.
async fn split_and_apply_routes(
    client: &NetworkClient,
    cfg: &PeerPollConfig,
    peers: &PeerSet,
) -> PeerSet {
    let mut routes = Vec::new();
    let mut wg_peers = Vec::new();
    for p in &peers.peers {
        match client
            .fetch_transport(&cfg.network_name, &cfg.node_id, &p.node_id)
            .await
        {
            Ok(pair) => match Transport::parse(&pair.transport) {
                Ok(Transport::Wireguard) => wg_peers.push(p.clone()),
                Ok(t) => {
                    let cidr = p.allowed_ips.first().cloned().unwrap_or_default();
                    routes.push(PeerRoute {
                        peer_node_id: p.node_id.clone(),
                        peer_cidr: cidr,
                        transport: t,
                    });
                }
                Err(err) => {
                    warn!(error = %err, peer = %p.node_id, "bad transport; defaulting to wireguard");
                    wg_peers.push(p.clone());
                }
            },
            Err(err) => {
                // Fail closed to encrypted mesh when transport lookup fails.
                warn!(error = %err, peer = %p.node_id, "transport lookup failed; defaulting to wireguard");
                wg_peers.push(p.clone());
            }
        }
    }
    if cfg.docker_colocated {
        if let Some(cidr) = cfg.local_cidr.as_deref() {
            routes.push(PeerRoute {
                peer_node_id: cfg.node_id.clone(),
                peer_cidr: cidr.to_string(),
                transport: Transport::Docker,
            });
        }
    }
    if !routes.is_empty() {
        if let Err(err) = apply_routes(
            cfg.route_backend.as_ref(),
            &cfg.node_id,
            cfg.local_cidr.as_deref(),
            &cfg.private_iface,
            &routes,
        ) {
            warn!(error = %err, "route apply failed");
        }
    }
    PeerSet {
        node_id: peers.node_id.clone(),
        peer_version: peers.peer_version,
        mtu: peers.mtu,
        peers: wg_peers,
    }
}

/// One poll cycle: fetch → split by transport → routes / WG apply → report.
pub async fn poll_once(client: &NetworkClient, cfg: &PeerPollConfig, last: &mut i64) {
    match client.fetch_peers(&cfg.network_name, &cfg.node_id).await {
        Ok(peers) => {
            let wg_peers = split_and_apply_routes(client, cfg, &peers).await;

            // Never start WireGuard when no pair resolves to wireguard.
            if !should_start_wireguard(!wg_peers.peers.is_empty()) {
                if peers.peer_version != *last {
                    debug!(
                        peer_version = peers.peer_version,
                        "no wireguard peers; skipping wg interface"
                    );
                    *last = peers.peer_version;
                }
                return;
            }

            if peers.peer_version == *last {
                debug!(peer_version = peers.peer_version, "peer set unchanged");
                return;
            }
            match cfg.backend.apply(&cfg.iface, &wg_peers) {
                Ok(()) => {
                    if let Err(err) = client
                        .report_applied(&cfg.network_name, &cfg.node_id, peers.peer_version)
                        .await
                    {
                        warn!(error = %err, "failed to report applied peer version");
                    } else {
                        info!(
                            peer_version = peers.peer_version,
                            peers = wg_peers.peers.len(),
                            backend = ?cfg.backend.kind(),
                            "applied wireguard peer set"
                        );
                        *last = peers.peer_version;
                    }
                }
                Err(err) => warn!(error = %err, "wireguard apply failed"),
            }
        }
        Err(err) => warn!(error = %err, "peer poll failed"),
    }
}

/// Spawn the register + poll loop. Returns a JoinHandle.
pub fn spawn_peer_poll_loop(cfg: PeerPollConfig) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let client = match NetworkClient::new(&cfg.network_url) {
            Ok(c) => c,
            Err(err) => {
                warn!(error = %err, "peer poll loop disabled");
                return;
            }
        };

        // Publish colocation / membership so TransportResolver can select modes.
        if cfg.docker_colocated || cfg.network_membership.is_some() {
            for attempt in 1..=5 {
                match client
                    .patch_membership(
                        &cfg.node_id,
                        cfg.network_membership.as_deref(),
                        if cfg.docker_colocated {
                            Some(true)
                        } else {
                            None
                        },
                    )
                    .await
                {
                    Ok(()) => {
                        info!(
                            node_id = %cfg.node_id,
                            docker_colocated = cfg.docker_colocated,
                            membership = ?cfg.network_membership,
                            "published network membership to forge-network"
                        );
                        break;
                    }
                    Err(err) => {
                        warn!(attempt, error = %err, "membership patch not ready; retrying");
                        sleep(Duration::from_secs(2)).await;
                    }
                }
            }
        }

        // Best-effort WG register (requires node lease). Skipped when node is
        // docker-colocated-only until a wireguard peer appears — still register
        // the key so mixed clusters can add WG peers later.
        for attempt in 1..=5 {
            match client
                .register_peer(
                    &cfg.network_name,
                    &cfg.node_id,
                    &cfg.public_key,
                    cfg.endpoint.as_deref(),
                )
                .await
            {
                Ok(()) => {
                    info!(
                        network = %cfg.network_name,
                        node_id = %cfg.node_id,
                        "registered wireguard peer with forge-network"
                    );
                    break;
                }
                Err(err) => {
                    warn!(
                        attempt,
                        error = %err,
                        "wireguard register not ready; retrying"
                    );
                    sleep(Duration::from_secs(2)).await;
                }
            }
        }

        let mut last = -1_i64;
        loop {
            poll_once(&client, &cfg, &mut last).await;
            sleep(cfg.poll_interval).await;
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use httpmock::prelude::*;

    #[tokio::test]
    async fn poll_once_applies_and_reports() {
        let server = MockServer::start();
        let peers_mock = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/networks/cluster-overlay/nodes/node-a/peers");
            then.status(200).json_body(serde_json::json!({
                "node_id": "node-a",
                "peer_version": 3,
                "peers": [{
                    "node_id": "node-b",
                    "public_key": "b64:b",
                    "endpoint": "1.1.1.1:51820",
                    "allowed_ips": ["10.100.2.0/24"],
                    "persistent_keepalive": 25
                }]
            }));
        });
        let applied_mock = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/networks/cluster-overlay/nodes/node-a/applied-version");
            then.status(200).json_body(serde_json::json!({
                "node_id": "node-a",
                "applied_peer_version": 3,
                "network_drift_total": 0
            }));
        });

        let backend = Arc::new(FakeWgBackend::new());
        let route_backend = Arc::new(FakeRouteBackend::new());
        let cfg = PeerPollConfig {
            network_url: server.base_url(),
            network_name: "cluster-overlay".into(),
            node_id: "node-a".into(),
            public_key: "b64:a".into(),
            endpoint: None,
            iface: "wg0".into(),
            poll_interval: Duration::from_secs(5),
            backend: backend.clone(),
            route_backend,
            docker_colocated: false,
            network_membership: None,
            private_iface: "eth1".into(),
            local_cidr: None,
        };
        let client = NetworkClient::new(&cfg.network_url).unwrap();
        let mut last = -1;
        poll_once(&client, &cfg, &mut last).await;
        assert_eq!(last, 3);
        assert_eq!(backend.peer_count(), 1);
        peers_mock.assert();
        applied_mock.assert();
    }
}
