mod key;

pub use key::{load_or_create_keypair, resolve_key_dir, NodePublicKey};

use crate::docker::DockerProbe;
use chrono::{DateTime, Utc};
use serde::Serialize;
use std::collections::HashMap;
use std::fs;
use std::io::Write;
use std::path::Path;
use tracing::{info, warn};
use uuid::Uuid;

const NODE_ID_FILENAME: &str = "node_id";
pub const NODE_ID_LABEL: &str = "forge.node_id";

/// Stable node identity and host facts advertised via `/v1/node`.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NodeInfo {
    pub id: String,
    pub hostname: String,
    pub docker_version: String,
    pub cpu: u32,
    pub memory_bytes: u64,
    pub disk_bytes: u64,
    pub started_at: DateTime<Utc>,
    /// WireGuard public key (`b64:...`); never includes the private key.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wireguard_public_key: Option<String>,
}

/// Runtime node: persisted id + gathered capacity/info + label helper.
#[derive(Debug, Clone)]
pub struct Node {
    pub info: NodeInfo,
    /// Public key only — private key never leaves the key module / disk.
    pub wireguard_public_key: Option<NodePublicKey>,
    /// Network-plane health (`Ready` / `Degraded` for DNS bootstrap failures) (22.06).
    pub network_status: std::sync::Arc<crate::network::NodeNetworkHealth>,
}

impl Node {
    /// Load or create a stable node id under `data_dir`, then gather host info.
    ///
    /// When `node_id_override` is set (`FORGE_NODE_ID`), that value is used as the
    /// stable id (enables multi-node demos with human-readable ids like `node-a`).
    pub async fn bootstrap(
        data_dir: impl AsRef<Path>,
        docker: &dyn DockerProbe,
    ) -> Result<Self, String> {
        Self::bootstrap_with_id(data_dir, docker, None).await
    }

    pub async fn bootstrap_with_id(
        data_dir: impl AsRef<Path>,
        docker: &dyn DockerProbe,
        node_id_override: Option<&str>,
    ) -> Result<Self, String> {
        Self::bootstrap_with_id_and_keys(data_dir, docker, node_id_override, None).await
    }

    pub async fn bootstrap_with_id_and_keys(
        data_dir: impl AsRef<Path>,
        docker: &dyn DockerProbe,
        node_id_override: Option<&str>,
        key_dir: Option<&Path>,
    ) -> Result<Self, String> {
        let data_dir = data_dir.as_ref();
        ensure_writable_data_dir(data_dir)?;

        let id = if let Some(override_id) = node_id_override
            .map(str::trim)
            .filter(|s| !s.is_empty())
        {
            info!(
                node_id = %override_id,
                data_dir = %data_dir.display(),
                "using FORGE_NODE_ID override"
            );
            override_id.to_string()
        } else {
            let (id, generated) = load_or_create_node_id(data_dir)?;
            if generated {
                info!(node_id = %id, data_dir = %data_dir.display(), "generated new node id");
            } else {
                info!(node_id = %id, data_dir = %data_dir.display(), "loaded persisted node id");
            }
            id
        };

        let key_dir_resolved = resolve_key_dir(key_dir, data_dir);
        let public_key = load_or_create_keypair(&key_dir_resolved)?;

        let docker_version = match docker.engine_version().await {
            Ok(v) if !v.trim().is_empty() => v,
            Ok(_) => {
                warn!("docker version empty; using unknown");
                "unknown".into()
            }
            Err(err) => {
                warn!(error = %err, "docker version lookup failed; using unknown");
                "unknown".into()
            }
        };

        let info = NodeInfo {
            id,
            hostname: hostname(),
            docker_version,
            cpu: cpu_count(),
            memory_bytes: memory_bytes(),
            disk_bytes: disk_bytes(),
            started_at: Utc::now(),
            wireguard_public_key: Some(public_key.as_str().to_string()),
        };

        Ok(Self {
            info,
            wireguard_public_key: Some(public_key),
            network_status: std::sync::Arc::new(crate::network::NodeNetworkHealth::new()),
        })
    }

    /// Label map for workload containers (`forge.node_id`).
    pub fn labels(&self) -> HashMap<String, String> {
        let mut labels = HashMap::new();
        labels.insert(NODE_ID_LABEL.to_string(), self.info.id.clone());
        labels
    }

    /// Network-plane status for `/v1/node` (`Ready` or `Degraded`).
    pub fn network_status_label(&self) -> &'static str {
        self.network_status.status()
    }
}

/// Resolve the address advertised to Control for this Runtime agent.
pub fn advertise_address(override_addr: Option<&str>, port: u16) -> String {
    if let Some(addr) = override_addr.map(str::trim).filter(|s| !s.is_empty()) {
        return addr.to_string();
    }
    format!("http://{}:{}", hostname(), port)
}

/// Host CPU architecture for scheduling (`amd64` / `arm64` / raw uname).
pub fn host_architecture() -> String {
    let raw = std::env::consts::ARCH;
    match raw {
        "x86_64" => "amd64".into(),
        "aarch64" => "arm64".into(),
        other => other.to_string(),
    }
}

/// Host OS for scheduling (lowercase; typically `linux`).
pub fn host_os() -> String {
    std::env::consts::OS.to_ascii_lowercase()
}

pub fn load_or_create_node_id(data_dir: &Path) -> Result<(String, bool), String> {
    let path = data_dir.join(NODE_ID_FILENAME);
    if path.exists() {
        let raw = fs::read_to_string(&path)
            .map_err(|e| format!("read node id {}: {e}", path.display()))?;
        let id = raw.trim().to_string();
        if id.is_empty() {
            return Err(format!("node id file {} is empty", path.display()));
        }
        Uuid::parse_str(&id)
            .map_err(|e| format!("node id file {} is not a valid uuid: {e}", path.display()))?;
        return Ok((id, false));
    }

    let id = Uuid::new_v4().to_string();
    write_node_id(&path, &id)?;
    Ok((id, true))
}

fn write_node_id(path: &Path, id: &str) -> Result<(), String> {
    let mut opts = fs::OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }

    let mut file = opts
        .open(path)
        .map_err(|e| format!("create node id {}: {e}", path.display()))?;
    file.write_all(id.as_bytes())
        .map_err(|e| format!("write node id {}: {e}", path.display()))?;
    file.write_all(b"\n")
        .map_err(|e| format!("write node id {}: {e}", path.display()))?;
    file.sync_all()
        .map_err(|e| format!("sync node id {}: {e}", path.display()))?;
    Ok(())
}

fn ensure_writable_data_dir(data_dir: &Path) -> Result<(), String> {
    fs::create_dir_all(data_dir).map_err(|e| {
        format!(
            "FORGE_RUNTIME_DATA_DIR {} is not creatable/writable: {e}",
            data_dir.display()
        )
    })?;

    let probe = data_dir.join(".write_probe");
    fs::write(&probe, b"ok").map_err(|e| {
        format!(
            "FORGE_RUNTIME_DATA_DIR {} is not writable: {e}",
            data_dir.display()
        )
    })?;
    let _ = fs::remove_file(&probe);
    Ok(())
}

fn hostname() -> String {
    hostname_impl().unwrap_or_else(|| "unknown".into())
}

fn hostname_impl() -> Option<String> {
    if let Ok(h) = std::env::var("HOSTNAME") {
        let trimmed = h.trim();
        if !trimmed.is_empty() {
            return Some(trimmed.to_string());
        }
    }
    fs::read_to_string("/etc/hostname")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

fn cpu_count() -> u32 {
    std::thread::available_parallelism()
        .map(|n| n.get() as u32)
        .unwrap_or(1)
}

fn memory_bytes() -> u64 {
    #[cfg(target_os = "linux")]
    {
        if let Ok(contents) = fs::read_to_string("/proc/meminfo") {
            for line in contents.lines() {
                if let Some(rest) = line.strip_prefix("MemTotal:") {
                    let kb: u64 = rest
                        .split_whitespace()
                        .next()
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(0);
                    return kb.saturating_mul(1024);
                }
            }
        }
        0
    }
    #[cfg(not(target_os = "linux"))]
    {
        // Best-effort outside Linux; macOS/dev hosts report 0 rather than inventing a value.
        0
    }
}

fn disk_bytes() -> u64 {
    // Prefer explicit override for demos/CI; otherwise best-effort Linux probe via `df`.
    if let Ok(raw) = std::env::var("FORGE_NODE_DISK_MB") {
        if let Ok(mb) = raw.trim().parse::<u64>() {
            return mb.saturating_mul(1024 * 1024);
        }
    }
    #[cfg(target_os = "linux")]
    {
        if let Ok(output) = std::process::Command::new("df")
            .args(["-B1", "-P", "/"])
            .output()
        {
            if output.status.success() {
                if let Ok(text) = String::from_utf8(output.stdout) {
                    // Filesystem 1024-blocks Used Available Capacity Mounted
                    if let Some(line) = text.lines().nth(1) {
                        let cols: Vec<&str> = line.split_whitespace().collect();
                        if cols.len() >= 2 {
                            if let Ok(total) = cols[1].parse::<u64>() {
                                return total;
                            }
                        }
                    }
                }
            }
        }
        0
    }
    #[cfg(not(target_os = "linux"))]
    {
        0
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::StubDocker;
    use std::os::unix::fs::PermissionsExt;
    use tempfile::tempdir;

    #[test]
    fn node_id_generated_once_and_reloaded() {
        let dir = tempdir().unwrap();
        let (id1, generated1) = load_or_create_node_id(dir.path()).unwrap();
        assert!(generated1);
        Uuid::parse_str(&id1).unwrap();

        let (id2, generated2) = load_or_create_node_id(dir.path()).unwrap();
        assert!(!generated2);
        assert_eq!(id1, id2);

        let path = dir.path().join(NODE_ID_FILENAME);
        let meta = fs::metadata(&path).unwrap();
        assert_eq!(meta.permissions().mode() & 0o777, 0o600);
    }

    #[test]
    fn unwritable_data_dir_fails_clearly() {
        let dir = tempdir().unwrap();
        // Use a regular file path as the data dir — create_dir_all fails for everyone,
        // including root (chmod 0555 is bypassed when the Docker build runs as uid 0).
        let not_a_dir = dir.path().join("not-a-directory");
        fs::write(&not_a_dir, b"x").unwrap();

        let err = ensure_writable_data_dir(&not_a_dir).expect_err("should fail");
        assert!(
            err.contains("not writable") || err.contains("not creatable"),
            "unexpected err: {err}"
        );
    }

    #[tokio::test]
    async fn node_info_serialization_shape() {
        let dir = tempdir().unwrap();
        let docker = StubDocker::ok("29.1.3");
        let node = Node::bootstrap(dir.path(), &docker).await.unwrap();

        let json = serde_json::to_value(&node.info).unwrap();
        assert!(json["id"].as_str().unwrap().len() > 10);
        assert!(json.get("hostname").is_some());
        assert_eq!(json["dockerVersion"], "29.1.3");
        assert!(json["cpu"].as_u64().unwrap() >= 1);
        assert!(json.get("memoryBytes").is_some());
        assert!(json.get("startedAt").is_some());
        assert!(json.get("docker_version").is_none());

        let labels = node.labels();
        assert_eq!(labels.get(NODE_ID_LABEL).unwrap(), &node.info.id);
    }

    #[tokio::test]
    async fn docker_version_unknown_on_failure() {
        let dir = tempdir().unwrap();
        let docker = StubDocker::down();
        let node = Node::bootstrap(dir.path(), &docker).await.unwrap();
        assert_eq!(node.info.docker_version, "unknown");
    }

    #[tokio::test]
    async fn bootstrap_respects_node_id_override() {
        let dir = tempdir().unwrap();
        let docker = StubDocker::ok("1");
        let node = Node::bootstrap_with_id(dir.path(), &docker, Some("node-a"))
            .await
            .unwrap();
        assert_eq!(node.info.id, "node-a");
    }

    #[test]
    fn advertise_address_prefers_override() {
        assert_eq!(
            advertise_address(Some("http://runtime-a:4102"), 8080),
            "http://runtime-a:4102"
        );
        let fallback = advertise_address(None, 4102);
        assert!(fallback.starts_with("http://"));
        assert!(fallback.ends_with(":4102"));
    }
}
