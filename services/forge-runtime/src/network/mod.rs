//! Runtime network module: WireGuard peer poll / apply / report (22.03).

mod wireguard;

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
}

/// HTTP client against forge-network peer APIs.
#[derive(Clone)]
pub struct NetworkClient {
    base_url: String,
    http: reqwest::Client,
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
}

fn enc(s: &str) -> String {
    urlencoding_lite(s)
}

fn urlencoding_lite(s: &str) -> String {
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

/// One poll cycle: fetch → diff version → apply → report.
pub async fn poll_once(client: &NetworkClient, cfg: &PeerPollConfig, last: &mut i64) {
    match client.fetch_peers(&cfg.network_name, &cfg.node_id).await {
        Ok(peers) => {
            if peers.peer_version == *last {
                debug!(peer_version = peers.peer_version, "peer set unchanged");
                return;
            }
            match cfg.backend.apply(&cfg.iface, &peers) {
                Ok(()) => {
                    if let Err(err) = client
                        .report_applied(&cfg.network_name, &cfg.node_id, peers.peer_version)
                        .await
                    {
                        warn!(error = %err, "failed to report applied peer version");
                    } else {
                        info!(
                            peer_version = peers.peer_version,
                            peers = peers.peers.len(),
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
        // Best-effort register (requires node lease already present from join).
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
        let cfg = PeerPollConfig {
            network_url: server.base_url(),
            network_name: "cluster-overlay".into(),
            node_id: "node-a".into(),
            public_key: "b64:a".into(),
            endpoint: None,
            iface: "wg0".into(),
            poll_interval: Duration::from_secs(5),
            backend: backend.clone(),
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
