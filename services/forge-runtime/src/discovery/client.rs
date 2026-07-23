use crate::observability;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;
use tracing::{debug, info, warn};

/// Outbound Discovery HTTP client for register / renew / deregister.
#[derive(Clone)]
pub struct DiscoveryClient {
    base_url: String,
    enabled: bool,
    node_id: String,
    lease_seconds: u32,
    http: reqwest::Client,
    /// endpoint id → (project, environment, service)
    registered: std::sync::Arc<Mutex<HashMap<String, Scope>>>,
}

#[derive(Debug, Clone)]
struct Scope {
    project: String,
    environment: String,
    #[allow(dead_code)]
    service: String,
}

#[derive(Debug, Clone)]
pub struct RegisterRequest {
    pub project: String,
    pub environment: String,
    pub service: String,
    pub id: String,
    pub address_ip: String,
    pub address_port: u16,
    pub protocol: String,
    pub revision: Option<String>,
}

#[derive(Debug, Clone)]
pub struct RenewRequest {
    pub project: String,
    pub environment: String,
    pub id: String,
    pub ready: bool,
}

#[derive(Debug, Clone)]
pub struct DeregisterTarget {
    pub project: String,
    pub environment: String,
    pub id: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DiscoveryError {
    Disabled,
    Unreachable(String),
    Http(u16, String),
    Decode(String),
}

impl std::fmt::Display for DiscoveryError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Disabled => write!(f, "discovery registration disabled"),
            Self::Unreachable(m) => write!(f, "discovery unreachable: {m}"),
            Self::Http(code, m) => write!(f, "discovery http {code}: {m}"),
            Self::Decode(m) => write!(f, "discovery decode: {m}"),
        }
    }
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct RegisterBody {
    id: String,
    node: String,
    address: AddressBody,
    protocol: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    revision: Option<String>,
    lease_seconds: u32,
}

#[derive(Debug, Serialize)]
struct AddressBody {
    ip: String,
    port: u16,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct RenewBody {
    ready: bool,
    lease_seconds: u32,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RegisterResponse {
    id: String,
    phase: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RenewResponse {
    id: String,
    phase: String,
}

impl DiscoveryClient {
    pub fn new(
        base_url: impl Into<String>,
        node_id: impl Into<String>,
        enabled: bool,
        lease_seconds: u32,
    ) -> Result<Self, String> {
        let base_url = base_url.into().trim().trim_end_matches('/').to_string();
        if enabled && base_url.is_empty() {
            return Err("FORGE_DISCOVERY_URL must not be empty when registration is enabled".into());
        }
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(5))
            .build()
            .map_err(|e| format!("build discovery http client: {e}"))?;
        Ok(Self {
            base_url,
            enabled,
            node_id: node_id.into(),
            lease_seconds: lease_seconds.max(1),
            http,
            registered: std::sync::Arc::new(Mutex::new(HashMap::new())),
        })
    }

    pub fn enabled(&self) -> bool {
        self.enabled
    }

    pub fn is_registered(&self, id: &str) -> bool {
        self.registered
            .lock()
            .expect("discovery registered")
            .contains_key(id)
    }

    /// Register after the first successful readiness probe (idempotent upsert).
    pub async fn register(&self, req: RegisterRequest) -> Result<(), DiscoveryError> {
        if !self.enabled {
            return Err(DiscoveryError::Disabled);
        }
        let url = format!(
            "{}/v1/projects/{}/environments/{}/services/{}/endpoints",
            self.base_url,
            path_seg(&req.project),
            path_seg(&req.environment),
            path_seg(&req.service)
        );
        let body = RegisterBody {
            id: req.id.clone(),
            node: self.node_id.clone(),
            address: AddressBody {
                ip: req.address_ip,
                port: req.address_port,
            },
            protocol: if req.protocol.is_empty() {
                "http".into()
            } else {
                req.protocol
            },
            revision: req.revision,
            lease_seconds: self.lease_seconds,
        };
        let resp: RegisterResponse = self.post_json(&url, &body).await?;
        self.registered.lock().expect("discovery registered").insert(
            req.id.clone(),
            Scope {
                project: req.project,
                environment: req.environment,
                service: req.service,
            },
        );
        info!(
            event = "runtime.discovery.registered",
            id = %resp.id,
            phase = %resp.phase,
            "registered endpoint with discovery"
        );
        Ok(())
    }

    /// Renew lease on each probe tick after registration.
    pub async fn renew(&self, req: RenewRequest) -> Result<(), DiscoveryError> {
        if !self.enabled {
            return Err(DiscoveryError::Disabled);
        }
        let url = format!(
            "{}/v1/projects/{}/environments/{}/endpoints/{}/renew",
            self.base_url,
            path_seg(&req.project),
            path_seg(&req.environment),
            path_seg(&req.id)
        );
        let body = RenewBody {
            ready: req.ready,
            lease_seconds: self.lease_seconds,
        };
        let resp: RenewResponse = self.post_json(&url, &body).await?;
        debug!(
            event = "runtime.discovery.renewed",
            id = %resp.id,
            phase = %resp.phase,
            ready = req.ready,
            "renewed discovery lease"
        );
        Ok(())
    }

    /// Deregister on graceful stop.
    pub async fn deregister(&self, target: DeregisterTarget) -> Result<(), DiscoveryError> {
        if !self.enabled {
            return Err(DiscoveryError::Disabled);
        }
        let url = format!(
            "{}/v1/projects/{}/environments/{}/endpoints/{}",
            self.base_url,
            path_seg(&target.project),
            path_seg(&target.environment),
            path_seg(&target.id)
        );
        let mut builder = self.http.delete(&url);
        builder = observability::inject_reqwest(builder);
        let resp = builder.send().await.map_err(|e| {
            DiscoveryError::Unreachable(e.to_string())
        })?;
        let status = resp.status().as_u16();
        if status == 204 || status == 404 {
            self.registered
                .lock()
                .expect("discovery registered")
                .remove(&target.id);
            info!(
                event = "runtime.discovery.deregistered",
                id = %target.id,
                "deregistered endpoint from discovery"
            );
            return Ok(());
        }
        let body = resp.text().await.unwrap_or_default();
        Err(DiscoveryError::Http(status, body))
    }

    /// Best-effort deregister for a known registered id (uses cached scope).
    pub async fn deregister_known(&self, id: &str) {
        let scope = {
            self.registered
                .lock()
                .expect("discovery registered")
                .get(id)
                .cloned()
        };
        let Some(scope) = scope else {
            return;
        };
        if let Err(err) = self
            .deregister(DeregisterTarget {
                project: scope.project,
                environment: scope.environment,
                id: id.to_string(),
            })
            .await
        {
            warn!(id = %id, error = %err, "discovery deregister failed");
        }
    }

    /// Deregister every tracked endpoint (process shutdown).
    pub async fn deregister_all(&self) {
        let ids: Vec<String> = self
            .registered
            .lock()
            .expect("discovery registered")
            .keys()
            .cloned()
            .collect();
        for id in ids {
            self.deregister_known(&id).await;
        }
    }

    async fn post_json<T: Serialize, R: for<'de> Deserialize<'de>>(
        &self,
        url: &str,
        body: &T,
    ) -> Result<R, DiscoveryError> {
        let raw =
            serde_json::to_vec(body).map_err(|e| DiscoveryError::Decode(e.to_string()))?;
        let resp = observability::inject_reqwest(
            self.http
                .post(url)
                .header("content-type", "application/json")
                .body(raw),
        )
        .send()
        .await
        .map_err(|e| DiscoveryError::Unreachable(e.to_string()))?;
        let status = resp.status().as_u16();
        let bytes = resp
            .bytes()
            .await
            .map_err(|e| DiscoveryError::Unreachable(e.to_string()))?;
        if !(200..300).contains(&status) {
            return Err(DiscoveryError::Http(
                status,
                String::from_utf8_lossy(&bytes).into_owned(),
            ));
        }
        serde_json::from_slice(&bytes).map_err(|e| DiscoveryError::Decode(e.to_string()))
    }
}

fn path_seg(s: &str) -> String {
    // Keep path segments simple; ids are replica/service names without `/`.
    s.trim().replace('/', "")
}

#[cfg(test)]
mod tests {
    use super::*;
    use httpmock::prelude::*;

    #[tokio::test]
    async fn register_renew_deregister_roundtrip() {
        let server = MockServer::start();
        let _reg = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/projects/demo/environments/local/services/demo-echo/endpoints");
            then.status(200).json_body(serde_json::json!({
                "id": "demo-echo-1",
                "service": "demo-echo",
                "phase": "Pending",
                "expiresAt": "2026-07-22T10:00:20Z"
            }));
        });
        let _ren = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/projects/demo/environments/local/endpoints/demo-echo-1/renew");
            then.status(200).json_body(serde_json::json!({
                "id": "demo-echo-1",
                "phase": "Ready",
                "expiresAt": "2026-07-22T10:00:40Z"
            }));
        });
        let _del = server.mock(|when, then| {
            when.method(DELETE)
                .path("/v1/projects/demo/environments/local/endpoints/demo-echo-1");
            then.status(204);
        });

        let client = DiscoveryClient::new(server.base_url(), "node-a", true, 20).unwrap();
        client
            .register(RegisterRequest {
                project: "demo".into(),
                environment: "local".into(),
                service: "demo-echo".into(),
                id: "demo-echo-1".into(),
                address_ip: "172.20.0.10".into(),
                address_port: 8080,
                protocol: "http".into(),
                revision: None,
            })
            .await
            .unwrap();
        assert!(client.is_registered("demo-echo-1"));
        client
            .renew(RenewRequest {
                project: "demo".into(),
                environment: "local".into(),
                id: "demo-echo-1".into(),
                ready: true,
            })
            .await
            .unwrap();
        client
            .deregister(DeregisterTarget {
                project: "demo".into(),
                environment: "local".into(),
                id: "demo-echo-1".into(),
            })
            .await
            .unwrap();
        assert!(!client.is_registered("demo-echo-1"));
    }
}
