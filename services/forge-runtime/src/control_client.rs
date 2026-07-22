use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::Duration;
use tracing::{debug, info, warn};

/// Desired deployment fetched from Control for this node.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DesiredDeployment {
    pub id: String,
    pub image: String,
    #[serde(default = "default_port")]
    pub port: u16,
    #[serde(default = "default_replicas")]
    pub desired_replicas: i32,
    #[serde(default)]
    pub service_id: Option<String>,
    #[serde(default)]
    pub environment_id: Option<String>,
    /// Optional env map; when absent Runtime synthesizes `PORT`.
    #[serde(default)]
    pub environment: HashMap<String, String>,
}

fn default_port() -> u16 {
    8080
}

fn default_replicas() -> i32 {
    1
}

impl DesiredDeployment {
    pub fn is_desired(&self) -> bool {
        self.desired_replicas > 0
    }

    pub fn workload_environment(&self) -> HashMap<String, String> {
        let mut env = self.environment.clone();
        env.entry("PORT".into())
            .or_insert_with(|| self.port.to_string());
        env
    }
}

/// Body for `POST /v1/deployments/{id}/status` (documented Control contract).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DeploymentStatusReport {
    pub status: String,
    pub node_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint: Option<EndpointReport>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EndpointReport {
    pub host_port: u16,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ControlError {
    Unreachable(String),
    Http(u16, String),
    Decode(String),
}

impl std::fmt::Display for ControlError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unreachable(m) => write!(f, "control unreachable: {m}"),
            Self::Http(code, m) => write!(f, "control http {code}: {m}"),
            Self::Decode(m) => write!(f, "control decode: {m}"),
        }
    }
}

/// HTTP client for Control desired-state fetch and actual-status report.
#[derive(Clone)]
pub struct ControlClient {
    base_url: String,
    node_id: String,
    http: reqwest::Client,
}

impl ControlClient {
    pub fn new(base_url: impl Into<String>, node_id: impl Into<String>) -> Result<Self, String> {
        let base_url = base_url.into().trim().trim_end_matches('/').to_string();
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
            http,
        })
    }

    pub fn node_id(&self) -> &str {
        &self.node_id
    }

    /// URL for the preferred desired-deployments contract.
    pub fn desired_url(&self) -> String {
        format!(
            "{}/v1/deployments?nodeId={}&desired=true",
            self.base_url,
            urlencoding_node_id(&self.node_id)
        )
    }

    /// URL for status push contract.
    pub fn status_url(&self, deployment_id: &str) -> String {
        format!(
            "{}/v1/deployments/{}/status",
            self.base_url,
            deployment_id.trim()
        )
    }

    /// Fetch desired deployments for this node.
    ///
    /// Prefers `GET /v1/deployments?nodeId=&desired=true`. When that endpoint is
    /// missing (404), falls back to walking existing Control project trees
    /// (`GET /v1/projects` + `?expand=tree`) — single-node epic treats all
    /// deployments with `desiredReplicas > 0` as assigned to this node.
    pub async fn fetch_desired(&self) -> Result<Vec<DesiredDeployment>, ControlError> {
        match self.fetch_desired_contract().await {
            Ok(list) => Ok(list),
            Err(ControlError::Http(404, _)) | Err(ControlError::Http(405, _)) => {
                info!(
                    node_id = %self.node_id,
                    "control desired-by-node endpoint missing; falling back to project tree walk"
                );
                self.fetch_desired_via_tree().await
            }
            Err(other) => Err(other),
        }
    }

    async fn fetch_desired_contract(&self) -> Result<Vec<DesiredDeployment>, ControlError> {
        let url = self.desired_url();
        debug!(%url, "fetching desired deployments from control");
        let resp = self
            .http
            .get(&url)
            .send()
            .await
            .map_err(|e| ControlError::Unreachable(e.to_string()))?;
        let status = resp.status();
        let body = resp
            .text()
            .await
            .map_err(|e| ControlError::Unreachable(e.to_string()))?;
        if !status.is_success() {
            return Err(ControlError::Http(status.as_u16(), body));
        }
        let list: Vec<DesiredDeployment> = serde_json::from_str(&body)
            .map_err(|e| ControlError::Decode(format!("{e}; body={body}")))?;
        Ok(list
            .into_iter()
            .filter(DesiredDeployment::is_desired)
            .collect())
    }

    async fn fetch_desired_via_tree(&self) -> Result<Vec<DesiredDeployment>, ControlError> {
        let projects_url = format!("{}/v1/projects", self.base_url);
        let projects: Vec<ProjectRef> = self.get_json(&projects_url).await?;
        let mut out = Vec::new();
        for project in projects {
            let tree_url = format!("{}/v1/projects/{}?expand=tree", self.base_url, project.id);
            let tree: ProjectTree = self.get_json(&tree_url).await?;
            for app in tree.applications {
                for service in app.services {
                    for dep in service.deployments {
                        if dep.desired_replicas <= 0 {
                            continue;
                        }
                        out.push(DesiredDeployment {
                            id: dep.id,
                            image: dep.image,
                            port: service.port,
                            desired_replicas: dep.desired_replicas,
                            service_id: Some(service.id.clone()),
                            environment_id: Some(dep.environment_id),
                            environment: HashMap::new(),
                        });
                    }
                }
            }
        }
        Ok(out)
    }

    /// Report actual status (+ optional endpoint) to Control.
    ///
    /// When Control has not implemented the status endpoint yet (404), returns
    /// `Ok(())` after logging — callers rely on `GET /v1/node/state` pull.
    pub async fn report_status(
        &self,
        deployment_id: &str,
        report: &DeploymentStatusReport,
    ) -> Result<(), ControlError> {
        let url = self.status_url(deployment_id);
        let body =
            serde_json::to_string(report).map_err(|e| ControlError::Decode(e.to_string()))?;
        debug!(%url, deployment_id, status = %report.status, "reporting status to control");
        let resp = self
            .http
            .post(&url)
            .header("content-type", "application/json")
            .body(body)
            .send()
            .await
            .map_err(|e| ControlError::Unreachable(e.to_string()))?;
        let status = resp.status();
        if status.is_success() || status.as_u16() == 204 {
            info!(
                deployment_id,
                status = %report.status,
                host_port = report.endpoint.as_ref().map(|e| e.host_port),
                "reported deployment status to control"
            );
            return Ok(());
        }
        let text = resp.text().await.unwrap_or_default();
        if status.as_u16() == 404 {
            warn!(
                deployment_id,
                "control status endpoint missing (404); pull via /v1/node/state is the interim"
            );
            return Ok(());
        }
        Err(ControlError::Http(status.as_u16(), text))
    }

    async fn get_json<T: for<'de> Deserialize<'de>>(&self, url: &str) -> Result<T, ControlError> {
        let resp = self
            .http
            .get(url)
            .send()
            .await
            .map_err(|e| ControlError::Unreachable(e.to_string()))?;
        let status = resp.status();
        let body = resp
            .text()
            .await
            .map_err(|e| ControlError::Unreachable(e.to_string()))?;
        if !status.is_success() {
            return Err(ControlError::Http(status.as_u16(), body));
        }
        serde_json::from_str(&body).map_err(|e| ControlError::Decode(format!("{e}; body={body}")))
    }
}

fn urlencoding_node_id(id: &str) -> String {
    // Node ids are UUIDs; keep a minimal encoder for query safety.
    id.chars()
        .map(|c| match c {
            'A'..='Z' | 'a'..='z' | '0'..='9' | '-' | '_' | '.' | '~' => c.to_string(),
            other => format!("%{:02X}", other as u8),
        })
        .collect()
}

#[derive(Debug, Deserialize)]
struct ProjectRef {
    id: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ProjectTree {
    #[serde(default)]
    applications: Vec<AppTree>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct AppTree {
    #[serde(default)]
    services: Vec<ServiceTree>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ServiceTree {
    id: String,
    port: u16,
    #[serde(default)]
    deployments: Vec<DeploymentNode>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct DeploymentNode {
    id: String,
    image: String,
    environment_id: String,
    #[serde(default = "default_replicas")]
    desired_replicas: i32,
}

#[cfg(test)]
mod tests {
    use super::*;
    use httpmock::prelude::*;

    #[test]
    fn desired_url_shape() {
        let client = ControlClient::new("http://control:8080/", "node-1").unwrap();
        assert_eq!(
            client.desired_url(),
            "http://control:8080/v1/deployments?nodeId=node-1&desired=true"
        );
        assert_eq!(
            client.status_url("dep-1"),
            "http://control:8080/v1/deployments/dep-1/status"
        );
    }

    #[test]
    fn status_report_serializes_camel_case() {
        let report = DeploymentStatusReport {
            status: "active".into(),
            node_id: "node-1".into(),
            endpoint: Some(EndpointReport { host_port: 49152 }),
        };
        let json = serde_json::to_value(&report).unwrap();
        assert_eq!(json["status"], "active");
        assert_eq!(json["nodeId"], "node-1");
        assert_eq!(json["endpoint"]["hostPort"], 49152);
    }

    #[tokio::test]
    async fn fetch_desired_contract_request_shape() {
        let server = MockServer::start();
        let mock = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/deployments")
                .query_param("nodeId", "node-abc")
                .query_param("desired", "true");
            then.status(200).json_body(serde_json::json!([
                {
                    "id": "dep-1",
                    "image": "localhost:5000/demo-go:latest",
                    "port": 8080,
                    "desiredReplicas": 1
                },
                {
                    "id": "dep-zero",
                    "image": "localhost:5000/demo-go:latest",
                    "desiredReplicas": 0
                }
            ]));
        });

        let client = ControlClient::new(server.base_url(), "node-abc").unwrap();
        let list = client.fetch_desired().await.unwrap();
        mock.assert();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].id, "dep-1");
        assert_eq!(list[0].port, 8080);
    }

    #[tokio::test]
    async fn fetch_desired_falls_back_to_tree_on_404() {
        let server = MockServer::start();
        let _missing = server.mock(|when, then| {
            when.method(GET).path("/v1/deployments");
            then.status(404).body("not found");
        });
        let _projects = server.mock(|when, then| {
            when.method(GET).path("/v1/projects");
            then.status(200)
                .json_body(serde_json::json!([{"id":"proj-1","name":"p","slug":"p"}]));
        });
        let _tree = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/projects/proj-1")
                .query_param("expand", "tree");
            then.status(200).json_body(serde_json::json!({
                "project": {"id":"proj-1","name":"p","slug":"p","createdAt":"t","updatedAt":"t"},
                "environments": [],
                "applications": [{
                    "id":"app-1","projectId":"proj-1","name":"a","createdAt":"t","updatedAt":"t",
                    "services": [{
                        "id":"svc-1","applicationId":"app-1","name":"api","port":8080,
                        "createdAt":"t","updatedAt":"t",
                        "deployments": [{
                            "id":"dep-tree","serviceId":"svc-1","environmentId":"env-1",
                            "image":"localhost:5000/demo-go:latest","desiredReplicas":1,
                            "status":"pending","createdAt":"t","updatedAt":"t"
                        }]
                    }]
                }]
            }));
        });

        let client = ControlClient::new(server.base_url(), "node-abc").unwrap();
        let list = client.fetch_desired().await.unwrap();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].id, "dep-tree");
        assert_eq!(list[0].port, 8080);
        assert_eq!(list[0].service_id.as_deref(), Some("svc-1"));
    }

    #[tokio::test]
    async fn report_status_posts_documented_shape() {
        let server = MockServer::start();
        let mock = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/deployments/dep-1/status")
                .header("content-type", "application/json")
                .json_body(serde_json::json!({
                    "status": "active",
                    "nodeId": "node-abc",
                    "endpoint": {"hostPort": 45555}
                }));
            then.status(200).json_body(serde_json::json!({"ok": true}));
        });

        let client = ControlClient::new(server.base_url(), "node-abc").unwrap();
        client
            .report_status(
                "dep-1",
                &DeploymentStatusReport {
                    status: "active".into(),
                    node_id: "node-abc".into(),
                    endpoint: Some(EndpointReport { host_port: 45555 }),
                },
            )
            .await
            .unwrap();
        mock.assert();
    }

    #[tokio::test]
    async fn report_status_tolerates_missing_endpoint() {
        let server = MockServer::start();
        let _mock = server.mock(|when, then| {
            when.method(POST).path("/v1/deployments/dep-1/status");
            then.status(404).body("missing");
        });
        let client = ControlClient::new(server.base_url(), "node-abc").unwrap();
        client
            .report_status(
                "dep-1",
                &DeploymentStatusReport {
                    status: "active".into(),
                    node_id: "node-abc".into(),
                    endpoint: None,
                },
            )
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn unreachable_control_returns_error() {
        let client = ControlClient::new("http://127.0.0.1:1", "node-abc").unwrap();
        let err = client.fetch_desired().await.expect_err("unreachable");
        assert!(matches!(err, ControlError::Unreachable(_)));
    }
}
