use crate::docker::{ContainerInspectInfo, CreateWorkloadParams, DockerEngine};
use crate::node::{Node, NODE_ID_LABEL};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::{Duration, Instant};
use tracing::{info, warn};

pub const DEPLOYMENT_ID_LABEL: &str = "forge.deployment_id";
pub const MANAGED_LABEL: &str = "forge.managed";
pub const MANAGED_LABEL_VALUE: &str = "true";

/// Incoming workload create body (snake_case per specs.md / contract).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct WorkloadSpec {
    pub deployment_id: String,
    pub image: String,
    pub port: u16,
    #[serde(default)]
    pub environment: HashMap<String, String>,
}

/// Workload mapping returned by create/get (camelCase).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct WorkloadView {
    pub deployment_id: String,
    pub container_id: String,
    pub host_port: u16,
    pub state: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub image: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WorkloadError {
    Validation(String),
    Pull(String),
    Create(String),
    Start(String),
    Inspect(String),
    NotFound(String),
}

impl WorkloadError {
    pub fn status_code(&self) -> axum::http::StatusCode {
        use axum::http::StatusCode;
        match self {
            Self::Validation(_) => StatusCode::BAD_REQUEST,
            Self::NotFound(_) => StatusCode::NOT_FOUND,
            Self::Pull(_) => StatusCode::BAD_GATEWAY,
            Self::Create(_) | Self::Start(_) | Self::Inspect(_) => {
                StatusCode::INTERNAL_SERVER_ERROR
            }
        }
    }

    pub fn code(&self) -> &'static str {
        match self {
            Self::Validation(_) => "validation_error",
            Self::NotFound(_) => "not_found",
            Self::Pull(_) => "image_pull_failed",
            Self::Create(_) => "container_create_failed",
            Self::Start(_) => "container_start_failed",
            Self::Inspect(_) => "container_inspect_failed",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            Self::Validation(m)
            | Self::Pull(m)
            | Self::Create(m)
            | Self::Start(m)
            | Self::Inspect(m)
            | Self::NotFound(m) => m,
        }
    }
}

/// Deterministic container name for a deployment.
pub fn container_name(deployment_id: &str) -> String {
    format!("forge-{deployment_id}")
}

/// Labels applied to every managed workload container.
pub fn workload_labels(deployment_id: &str, node_id: &str) -> HashMap<String, String> {
    let mut labels = HashMap::new();
    labels.insert(DEPLOYMENT_ID_LABEL.to_string(), deployment_id.to_string());
    labels.insert(NODE_ID_LABEL.to_string(), node_id.to_string());
    labels.insert(MANAGED_LABEL.to_string(), MANAGED_LABEL_VALUE.to_string());
    labels
}

/// Validate the create spec. Returns normalized spec on success.
pub fn validate_spec(spec: &WorkloadSpec) -> Result<WorkloadSpec, WorkloadError> {
    let deployment_id = spec.deployment_id.trim().to_string();
    if deployment_id.is_empty() {
        return Err(WorkloadError::Validation(
            "deployment_id is required".into(),
        ));
    }
    if !is_valid_deployment_id(&deployment_id) {
        return Err(WorkloadError::Validation(format!(
            "deployment_id contains invalid characters: {deployment_id:?}"
        )));
    }

    let image = spec.image.trim().to_string();
    if image.is_empty() {
        return Err(WorkloadError::Validation("image is required".into()));
    }
    if !is_valid_image_ref(&image) {
        return Err(WorkloadError::Validation(format!(
            "image reference is invalid: {image:?}"
        )));
    }

    if spec.port == 0 {
        return Err(WorkloadError::Validation(
            "port must be an integer 1–65535".into(),
        ));
    }

    Ok(WorkloadSpec {
        deployment_id,
        image,
        port: spec.port,
        environment: spec.environment.clone(),
    })
}

fn is_valid_deployment_id(id: &str) -> bool {
    // Docker name fragment: keep forge-<id> within Docker's name rules.
    if id.len() > 200 {
        return false;
    }
    id.chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.')
        && id.chars().next().is_some_and(|c| c.is_ascii_alphanumeric())
}

fn is_valid_image_ref(image: &str) -> bool {
    if image.len() > 256 || image.contains(char::is_whitespace) {
        return false;
    }
    // Must look like a registry/repo[:tag] or repo[:tag] reference.
    let base = image.split('@').next().unwrap_or(image);
    if base.is_empty() || base.starts_with('/') || base.ends_with('/') {
        return false;
    }
    base.chars()
        .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-' | '/' | ':' | '+'))
}

/// Pull → create → start a workload. Cleans up partial containers on failure.
pub async fn create_and_start(
    docker: &dyn DockerEngine,
    node: &Node,
    spec: WorkloadSpec,
    pull_timeout: Duration,
) -> Result<WorkloadView, WorkloadError> {
    let spec = validate_spec(&spec)?;
    let name = container_name(&spec.deployment_id);
    let labels = workload_labels(&spec.deployment_id, &node.info.id);
    let env_keys: Vec<&str> = spec.environment.keys().map(String::as_str).collect();

    info!(
        deployment_id = %spec.deployment_id,
        image = %spec.image,
        container_port = spec.port,
        env_keys = ?env_keys,
        "workload create requested"
    );

    let pull_started = Instant::now();
    info!(deployment_id = %spec.deployment_id, image = %spec.image, "image pull starting");
    docker
        .pull_image(&spec.image, pull_timeout)
        .await
        .map_err(WorkloadError::Pull)?;
    info!(
        deployment_id = %spec.deployment_id,
        image = %spec.image,
        duration_ms = pull_started.elapsed().as_millis() as u64,
        "image pull finished"
    );

    let params = CreateWorkloadParams {
        name: name.clone(),
        image: spec.image.clone(),
        container_port: spec.port,
        env: spec.environment.clone(),
        labels,
    };

    let container_id = match docker.create_container(&params).await {
        Ok(id) => id,
        Err(err) => {
            warn!(
                deployment_id = %spec.deployment_id,
                error = %err,
                "container create failed"
            );
            return Err(WorkloadError::Create(err));
        }
    };

    info!(
        deployment_id = %spec.deployment_id,
        container_id = %container_id,
        name = %name,
        "container created"
    );

    if let Err(err) = docker.start_container(&container_id).await {
        warn!(
            deployment_id = %spec.deployment_id,
            container_id = %container_id,
            error = %err,
            "container start failed; removing partial container"
        );
        cleanup_container(docker, &container_id).await;
        return Err(WorkloadError::Start(err));
    }

    info!(
        deployment_id = %spec.deployment_id,
        container_id = %container_id,
        "container started"
    );

    let inspect = match docker.inspect_container(&container_id).await {
        Ok(info) => info,
        Err(err) => {
            warn!(
                deployment_id = %spec.deployment_id,
                container_id = %container_id,
                error = %err,
                "inspect after start failed; removing container"
            );
            cleanup_container(docker, &container_id).await;
            return Err(WorkloadError::Inspect(err));
        }
    };

    let host_port = match host_port_for(&inspect, spec.port) {
        Some(p) => p,
        None => {
            let msg = format!("no host port published for container port {}", spec.port);
            warn!(
                deployment_id = %spec.deployment_id,
                container_id = %container_id,
                "{msg}; removing container"
            );
            cleanup_container(docker, &container_id).await;
            return Err(WorkloadError::Inspect(msg));
        }
    };

    info!(
        deployment_id = %spec.deployment_id,
        container_id = %container_id,
        host_port,
        "host port assigned"
    );

    Ok(WorkloadView {
        deployment_id: spec.deployment_id,
        container_id,
        host_port,
        state: "starting".into(),
        image: Some(spec.image),
    })
}

/// Look up a workload by deployment id (Docker name / labels).
pub async fn get_workload(
    docker: &dyn DockerEngine,
    deployment_id: &str,
) -> Result<WorkloadView, WorkloadError> {
    let deployment_id = deployment_id.trim();
    if deployment_id.is_empty() || !is_valid_deployment_id(deployment_id) {
        return Err(WorkloadError::Validation("deployment_id is invalid".into()));
    }

    let name = container_name(deployment_id);
    let inspect = docker.inspect_container(&name).await.map_err(|err| {
        if err.contains("not found") || err.contains("No such container") {
            WorkloadError::NotFound(format!("no workload for deployment_id {deployment_id}"))
        } else {
            WorkloadError::Inspect(err)
        }
    })?;

    // Require Forge-managed labels when present so name collisions with unmanaged
    // containers are not reported as workloads.
    if let Some(labels) = &inspect.labels {
        if labels.get(MANAGED_LABEL).map(String::as_str) != Some(MANAGED_LABEL_VALUE) {
            return Err(WorkloadError::NotFound(format!(
                "no managed workload for deployment_id {deployment_id}"
            )));
        }
        if let Some(id) = labels.get(DEPLOYMENT_ID_LABEL) {
            if id != deployment_id {
                return Err(WorkloadError::NotFound(format!(
                    "no workload for deployment_id {deployment_id}"
                )));
            }
        }
    }

    let container_port = infer_container_port(&inspect).unwrap_or(0);
    let host_port = host_port_for(&inspect, container_port).unwrap_or(0);
    let state = map_docker_state(&inspect.state);

    Ok(WorkloadView {
        deployment_id: deployment_id.to_string(),
        container_id: inspect.id,
        host_port,
        state,
        image: inspect.image,
    })
}

fn host_port_for(inspect: &ContainerInspectInfo, container_port: u16) -> Option<u16> {
    if container_port == 0 {
        // Fall back to first published tcp port.
        return inspect.port_bindings.values().flatten().copied().next();
    }
    let key = format!("{container_port}/tcp");
    inspect
        .port_bindings
        .get(&key)
        .and_then(|ports| ports.first().copied())
}

fn infer_container_port(inspect: &ContainerInspectInfo) -> Option<u16> {
    inspect
        .port_bindings
        .keys()
        .find_map(|k| k.strip_suffix("/tcp").and_then(|p| p.parse::<u16>().ok()))
}

/// Coarse Docker→workload state for GET /v1/workloads (detailed status is /status).
fn map_docker_state(docker_state: &str) -> String {
    match docker_state.to_ascii_lowercase().as_str() {
        "running" | "created" | "restarting" => "starting".into(),
        "exited" | "dead" => "failed".into(),
        "paused" | "removing" => "stopped".into(),
        "" => "starting".into(),
        other => other.to_string(),
    }
}

async fn cleanup_container(docker: &dyn DockerEngine, id_or_name: &str) {
    if let Err(err) = docker.remove_container(id_or_name, true).await {
        warn!(
            container = %id_or_name,
            error = %err,
            "failed to remove partial container during cleanup"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::{RecordingDocker, StubDocker};
    use crate::node::Node;
    use std::sync::atomic::Ordering;
    use std::sync::Arc;
    use tempfile::tempdir;

    #[test]
    fn name_and_labels_are_deterministic() {
        let name = container_name("deployment-123");
        assert_eq!(name, "forge-deployment-123");

        let labels = workload_labels("deployment-123", "node-abc");
        assert_eq!(
            labels.get(DEPLOYMENT_ID_LABEL).map(String::as_str),
            Some("deployment-123")
        );
        assert_eq!(
            labels.get(NODE_ID_LABEL).map(String::as_str),
            Some("node-abc")
        );
        assert_eq!(labels.get(MANAGED_LABEL).map(String::as_str), Some("true"));
    }

    #[test]
    fn validate_spec_requires_image_and_port() {
        let err = validate_spec(&WorkloadSpec {
            deployment_id: "dep-1".into(),
            image: "".into(),
            port: 8080,
            environment: HashMap::new(),
        })
        .expect_err("image");
        assert!(matches!(err, WorkloadError::Validation(_)));

        let err = validate_spec(&WorkloadSpec {
            deployment_id: "dep-1".into(),
            image: "localhost:5000/demo-go:latest".into(),
            port: 0,
            environment: HashMap::new(),
        })
        .expect_err("port");
        assert!(matches!(err, WorkloadError::Validation(_)));
    }

    #[test]
    fn validate_spec_accepts_contract_shape() {
        let spec = validate_spec(&WorkloadSpec {
            deployment_id: "deployment-123".into(),
            image: "localhost:5000/demo-go:latest".into(),
            port: 8080,
            environment: HashMap::from([("FORGE_ENV".into(), "development".into())]),
        })
        .unwrap();
        assert_eq!(spec.deployment_id, "deployment-123");
        assert_eq!(spec.port, 8080);
    }

    #[test]
    fn response_serialization_is_camel_case() {
        let view = WorkloadView {
            deployment_id: "deployment-123".into(),
            container_id: "abc123".into(),
            host_port: 49152,
            state: "starting".into(),
            image: Some("localhost:5000/demo-go:latest".into()),
        };
        let json = serde_json::to_value(&view).unwrap();
        assert_eq!(json["deploymentId"], "deployment-123");
        assert_eq!(json["containerId"], "abc123");
        assert_eq!(json["hostPort"], 49152);
        assert_eq!(json["state"], "starting");
        assert_eq!(json["image"], "localhost:5000/demo-go:latest");
        assert!(json.get("deployment_id").is_none());
    }

    #[test]
    fn request_deserializes_snake_case_contract() {
        let raw = r#"{
            "deployment_id":"deployment-123",
            "image":"localhost:5000/demo-go:latest",
            "port":8080,
            "environment":{"FORGE_ENV":"development"}
        }"#;
        let spec: WorkloadSpec = serde_json::from_str(raw).unwrap();
        assert_eq!(spec.deployment_id, "deployment-123");
        assert_eq!(spec.port, 8080);
        assert_eq!(
            spec.environment.get("FORGE_ENV").map(String::as_str),
            Some("development")
        );
    }

    #[tokio::test]
    async fn create_orchestrates_pull_create_start() {
        let docker = RecordingDocker::ok(49152);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();

        let view = create_and_start(
            &docker,
            &node,
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::from([("FORGE_ENV".into(), "development".into())]),
            },
            Duration::from_secs(30),
        )
        .await
        .unwrap();

        assert_eq!(view.deployment_id, "deployment-123");
        assert_eq!(view.host_port, 49152);
        assert_eq!(view.state, "starting");
        assert_eq!(
            docker.calls.lock().unwrap().as_slice(),
            ["pull", "create", "start", "inspect"]
        );
        assert_eq!(
            docker.created_name.lock().unwrap().as_deref(),
            Some("forge-deployment-123")
        );
        let labels = docker.created_labels.lock().unwrap().clone();
        assert_eq!(
            labels.get(DEPLOYMENT_ID_LABEL).map(String::as_str),
            Some("deployment-123")
        );
        assert_eq!(
            labels.get(NODE_ID_LABEL).map(String::as_str),
            Some(node.info.id.as_str())
        );
        assert_eq!(labels.get(MANAGED_LABEL).map(String::as_str), Some("true"));
    }

    #[tokio::test]
    async fn create_cleans_up_when_start_fails() {
        let docker = RecordingDocker::fail_on("start");
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();

        let err = create_and_start(
            &docker,
            &node,
            WorkloadSpec {
                deployment_id: "deployment-xyz".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
            },
            Duration::from_secs(30),
        )
        .await
        .expect_err("start should fail");
        assert!(matches!(err, WorkloadError::Start(_)));
        assert_eq!(
            docker.calls.lock().unwrap().as_slice(),
            ["pull", "create", "start", "remove"]
        );
        assert_eq!(docker.remove_calls.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn create_does_not_create_when_pull_fails() {
        let docker = RecordingDocker::fail_on("pull");
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();

        let err = create_and_start(
            &docker,
            &node,
            WorkloadSpec {
                deployment_id: "deployment-xyz".into(),
                image: "localhost:5000/does-not-exist:latest".into(),
                port: 8080,
                environment: HashMap::new(),
            },
            Duration::from_secs(5),
        )
        .await
        .expect_err("pull should fail");
        assert!(matches!(err, WorkloadError::Pull(_)));
        assert_eq!(docker.calls.lock().unwrap().as_slice(), ["pull"]);
        assert_eq!(docker.remove_calls.load(Ordering::SeqCst), 0);
    }

    #[tokio::test]
    async fn get_workload_not_found() {
        let docker = RecordingDocker::missing();
        let err = get_workload(&docker, "missing-dep").await.expect_err("404");
        assert!(matches!(err, WorkloadError::NotFound(_)));
    }

    #[tokio::test]
    async fn get_workload_returns_mapping() {
        let docker = RecordingDocker::ok(45555);
        // Seed inspect path by creating first.
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        create_and_start(
            &docker,
            &node,
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
            },
            Duration::from_secs(5),
        )
        .await
        .unwrap();

        let view = get_workload(&docker, "deployment-123").await.unwrap();
        assert_eq!(view.host_port, 45555);
        assert_eq!(view.state, "starting");
        assert_eq!(view.image.as_deref(), Some("localhost:5000/demo-go:latest"));
        let _ = Arc::new(docker);
    }
}
