use crate::docker::DockerEngine;
use serde::Serialize;
use std::sync::Arc;
use std::time::Duration;
use tokio::task::JoinHandle;
use tracing::{info, warn};

/// Capacity advertised at Control registration.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct CapacityReport {
    pub slots: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cpu_millis: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mem_mb: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub disk_mb: Option<u32>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
struct RegisterBody {
    node_id: String,
    address: String,
    capacity: CapacityReport,
    #[serde(skip_serializing_if = "Option::is_none")]
    bootstrap_token: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    wireguard_public_key: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
struct ResourceSlots {
    slots: u32,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
struct HeartbeatBody {
    allocated: ResourceSlots,
    free: ResourceSlots,
    running_replicas: Vec<String>,
}

/// Registers with Control and periodically reports allocation heartbeats.
pub struct HeartbeatReporter {
    base_url: String,
    node_id: String,
    address: String,
    capacity: CapacityReport,
    bootstrap_token: Option<String>,
    wireguard_public_key: Option<String>,
    docker: Arc<dyn DockerEngine>,
    http: reqwest::Client,
}

impl HeartbeatReporter {
    pub fn new(
        control_url: impl Into<String>,
        node_id: impl Into<String>,
        address: impl Into<String>,
        capacity: CapacityReport,
        docker: Arc<dyn DockerEngine>,
    ) -> Result<Self, String> {
        Self::new_with_join(control_url, node_id, address, capacity, None, None, docker)
    }

    pub fn new_with_join(
        control_url: impl Into<String>,
        node_id: impl Into<String>,
        address: impl Into<String>,
        capacity: CapacityReport,
        bootstrap_token: Option<String>,
        wireguard_public_key: Option<String>,
        docker: Arc<dyn DockerEngine>,
    ) -> Result<Self, String> {
        let base_url = control_url.into().trim().trim_end_matches('/').to_string();
        if base_url.is_empty() {
            return Err("FORGE_CONTROL_URL must not be empty".into());
        }
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .map_err(|e| format!("build http client: {e}"))?;
        Ok(Self {
            base_url,
            node_id: node_id.into(),
            address: address.into(),
            capacity,
            bootstrap_token,
            wireguard_public_key,
            docker,
            http,
        })
    }

    pub fn register_url(&self) -> String {
        format!("{}/v1/nodes/register", self.base_url)
    }

    pub fn heartbeat_url(&self) -> String {
        format!("{}/v1/nodes/{}/heartbeat", self.base_url, self.node_id)
    }

    pub async fn register_once(&self) -> Result<(), String> {
        let body = RegisterBody {
            node_id: self.node_id.clone(),
            address: self.address.clone(),
            capacity: self.capacity.clone(),
            bootstrap_token: self.bootstrap_token.clone(),
            wireguard_public_key: self.wireguard_public_key.clone(),
        };
        let payload =
            serde_json::to_string(&body).map_err(|e| format!("register encode: {e}"))?;
        // Never log the bootstrap token or any private key material.
        let resp = self
            .http
            .post(self.register_url())
            .header("content-type", "application/json")
            .body(payload)
            .send()
            .await
            .map_err(|e| format!("register unreachable: {e}"))?;
        let status = resp.status();
        if status.is_success() {
            info!(
                node_id = %self.node_id,
                address = %self.address,
                slots = self.capacity.slots,
                has_bootstrap_token = self.bootstrap_token.is_some(),
                has_wireguard_public_key = self.wireguard_public_key.is_some(),
                "registered node with control"
            );
            Ok(())
        } else {
            let text = resp.text().await.unwrap_or_default();
            Err(format!("register http {}: {text}", status.as_u16()))
        }
    }

    pub async fn heartbeat_once(&self) -> Result<(), String> {
        let running = self.collect_running_replicas().await;
        let allocated = running.len() as u32;
        let free = self.capacity.slots.saturating_sub(allocated);
        let body = HeartbeatBody {
            allocated: ResourceSlots { slots: allocated },
            free: ResourceSlots { slots: free },
            running_replicas: running,
        };
        let payload =
            serde_json::to_string(&body).map_err(|e| format!("heartbeat encode: {e}"))?;
        let resp = self
            .http
            .post(self.heartbeat_url())
            .header("content-type", "application/json")
            .body(payload)
            .send()
            .await
            .map_err(|e| format!("heartbeat unreachable: {e}"))?;
        let status = resp.status();
        if status.is_success() {
            Ok(())
        } else {
            let text = resp.text().await.unwrap_or_default();
            Err(format!("heartbeat http {}: {text}", status.as_u16()))
        }
    }

    async fn collect_running_replicas(&self) -> Vec<String> {
        match self.docker.list_managed_containers().await {
            Ok(list) => {
                let mut out = Vec::new();
                for c in list {
                    let state = c.state.to_ascii_lowercase();
                    if state != "running" && state != "restarting" {
                        continue;
                    }
                    let labels = c.labels.unwrap_or_default();
                    // Shared Docker socket: only count workloads labeled for this node.
                    match labels.get(crate::node::NODE_ID_LABEL).map(String::as_str) {
                        Some(id) if id == self.node_id => {}
                        _ => continue,
                    }
                    let replica = replica_key(&labels, &c.id);
                    out.push(replica);
                }
                out.sort();
                out
            }
            Err(err) => {
                warn!(error = %err, "failed to list managed containers for heartbeat");
                Vec::new()
            }
        }
    }

    /// Spawn supervised register + heartbeat loop. Never crashes the process.
    pub fn spawn(self: Arc<Self>, interval: Duration) -> JoinHandle<()> {
        tokio::spawn(async move {
            loop {
                let inner = Arc::clone(&self);
                let result = tokio::task::spawn(async move {
                    run_loop(inner, interval).await;
                })
                .await;
                match result {
                    Ok(()) => warn!("control heartbeat reporter ended; restarting"),
                    Err(err) => warn!(error = %err, "control heartbeat reporter panicked; restarting"),
                }
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
        })
    }
}

async fn run_loop(reporter: Arc<HeartbeatReporter>, interval: Duration) {
    // Best-effort register until Control is up.
    loop {
        match reporter.register_once().await {
            Ok(()) => break,
            Err(err) => {
                warn!(error = %err, "node register failed; retrying");
                tokio::time::sleep(Duration::from_secs(2)).await;
            }
        }
    }

    let mut ticker = tokio::time::interval(interval);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    ticker.tick().await;
    if let Err(err) = reporter.heartbeat_once().await {
        warn!(error = %err, "initial node heartbeat failed");
    }

    loop {
        ticker.tick().await;
        if let Err(err) = reporter.heartbeat_once().await {
            warn!(error = %err, "node heartbeat failed");
        }
    }
}

fn replica_key(labels: &std::collections::HashMap<String, String>, fallback_id: &str) -> String {
    let deployment = labels
        .get("forge.deployment")
        .cloned()
        .or_else(|| labels.get("forge.deployment_id").cloned())
        .unwrap_or_else(|| fallback_id.to_string());
    if let Some(replica) = labels.get("forge.replica") {
        format!("{deployment}:{replica}")
    } else {
        deployment
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::StubDocker;
    use httpmock::prelude::*;
    use std::collections::HashMap;

    #[test]
    fn urls_shape() {
        let docker: Arc<dyn DockerEngine> = Arc::new(StubDocker::ok("1"));
        let reporter = HeartbeatReporter::new(
            "http://control:8080/",
            "node-a",
            "http://runtime-a:4102",
            CapacityReport {
                slots: 4,
                cpu_millis: Some(4000),
                mem_mb: Some(4096),
                disk_mb: None,
            },
            docker,
        )
        .unwrap();
        assert_eq!(reporter.register_url(), "http://control:8080/v1/nodes/register");
        assert_eq!(
            reporter.heartbeat_url(),
            "http://control:8080/v1/nodes/node-a/heartbeat"
        );
    }

    #[tokio::test]
    async fn register_and_heartbeat_payload_shape() {
        let server = MockServer::start();
        let register = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/nodes/register")
                .header("content-type", "application/json")
                .json_body(serde_json::json!({
                    "node_id": "node-a",
                    "address": "http://runtime-a:4102",
                    "capacity": {"slots": 4, "cpu_millis": 4000, "mem_mb": 4096}
                }));
            then.status(201).json_body(serde_json::json!({"ok": true}));
        });
        let heartbeat = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/nodes/node-a/heartbeat")
                .json_body(serde_json::json!({
                    "allocated": {"slots": 0},
                    "free": {"slots": 4},
                    "running_replicas": []
                }));
            then.status(200).json_body(serde_json::json!({"ok": true}));
        });

        let docker: Arc<dyn DockerEngine> = Arc::new(StubDocker::ok("1"));
        let reporter = Arc::new(
            HeartbeatReporter::new(
                server.base_url(),
                "node-a",
                "http://runtime-a:4102",
                CapacityReport {
                    slots: 4,
                    cpu_millis: Some(4000),
                    mem_mb: Some(4096),
                    disk_mb: None,
                },
                docker,
            )
            .unwrap(),
        );
        reporter.register_once().await.unwrap();
        reporter.heartbeat_once().await.unwrap();
        register.assert();
        heartbeat.assert();
    }

    #[test]
    fn replica_key_prefers_deployment_and_replica_labels() {
        let mut labels = HashMap::new();
        labels.insert("forge.deployment".into(), "dpl_1".into());
        labels.insert("forge.replica".into(), "0".into());
        assert_eq!(replica_key(&labels, "/forge-x"), "dpl_1:0");
    }
}
