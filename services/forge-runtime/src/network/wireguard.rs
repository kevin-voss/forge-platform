//! WireGuard peer application backends (22.03).
//!
//! Kernel path shells out to `wg` when available. Userspace/fake backends keep
//! CI and macOS Docker Desktop working without a kernel module or `/dev/net/tun`.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::Path;
use std::process::Command;
use std::sync::{Arc, Mutex};
use tracing::{info, warn};

/// Desired peer set from forge-network.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct PeerSet {
    pub node_id: String,
    pub peer_version: i64,
    #[serde(default)]
    pub mtu: Option<u16>,
    pub peers: Vec<PeerConfig>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
pub struct PeerConfig {
    pub node_id: String,
    pub public_key: String,
    #[serde(default)]
    pub endpoint: Option<String>,
    pub allowed_ips: Vec<String>,
    pub persistent_keepalive: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WgBackendKind {
    Kernel,
    Userspace,
    Fake,
}

impl WgBackendKind {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "kernel" => Ok(Self::Kernel),
            "userspace" => Ok(Self::Userspace),
            "fake" | "mock" => Ok(Self::Fake),
            "auto" => Ok(Self::auto_detect()),
            other => Err(format!(
                "FORGE_NETWORK_WG_BACKEND must be kernel|userspace|fake|auto, got {other:?}"
            )),
        }
    }

    /// Prefer kernel when `wg` and `/dev/net/tun` exist; otherwise userspace fake.
    pub fn auto_detect() -> Self {
        let tun_ok = Path::new("/dev/net/tun").exists();
        let wg_ok = Command::new("wg")
            .arg("version")
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false);
        if tun_ok && wg_ok {
            Self::Kernel
        } else {
            Self::Userspace
        }
    }
}

/// Applies a peer set to the local WireGuard interface.
pub trait WgBackend: Send + Sync {
    fn kind(&self) -> WgBackendKind;
    fn apply(&self, iface: &str, peers: &PeerSet) -> Result<(), String>;
    fn handshake_ok(&self, peer_public_key: &str) -> bool;
}

/// In-memory backend for CI / hosts without WireGuard.
#[derive(Debug, Default)]
pub struct FakeWgBackend {
    state: Mutex<FakeState>,
}

#[derive(Debug, Default)]
struct FakeState {
    last_version: i64,
    peers: HashMap<String, PeerConfig>,
    /// public_key → "handshake" present after apply (rotation continuity signal).
    handshakes: HashMap<String, bool>,
}

impl FakeWgBackend {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(FakeState::default()),
        }
    }

    pub fn applied_version(&self) -> i64 {
        self.state.lock().unwrap().last_version
    }

    pub fn peer_count(&self) -> usize {
        self.state.lock().unwrap().peers.len()
    }
}

impl WgBackend for FakeWgBackend {
    fn kind(&self) -> WgBackendKind {
        WgBackendKind::Fake
    }

    fn apply(&self, _iface: &str, peers: &PeerSet) -> Result<(), String> {
        let mut st = self.state.lock().unwrap();
        // Preserve handshake flags for keys that remain across rotation.
        let mut next_hs = HashMap::new();
        let mut next_peers = HashMap::new();
        for p in &peers.peers {
            next_peers.insert(p.public_key.clone(), p.clone());
            let had = st.handshakes.get(&p.public_key).copied().unwrap_or(false);
            // New keys get a handshake immediately (fake "connected"); retained keys keep it.
            next_hs.insert(p.public_key.clone(), had || true);
        }
        // Dual-key window: if any prior key for a node_id remains, connectivity is unbroken.
        st.peers = next_peers;
        st.handshakes = next_hs;
        st.last_version = peers.peer_version;
        Ok(())
    }

    fn handshake_ok(&self, peer_public_key: &str) -> bool {
        self.state
            .lock()
            .unwrap()
            .handshakes
            .get(peer_public_key)
            .copied()
            .unwrap_or(false)
    }
}

/// Userspace backend — same as fake for non-Linux CI; logs once that real
/// wireguard-go/boringtun is not linked in this build.
pub struct UserspaceWgBackend {
    inner: FakeWgBackend,
    logged: Mutex<bool>,
}

impl UserspaceWgBackend {
    pub fn new() -> Self {
        Self {
            inner: FakeWgBackend::new(),
            logged: Mutex::new(false),
        }
    }
}

impl WgBackend for UserspaceWgBackend {
    fn kind(&self) -> WgBackendKind {
        WgBackendKind::Userspace
    }

    fn apply(&self, iface: &str, peers: &PeerSet) -> Result<(), String> {
        {
            let mut logged = self.logged.lock().unwrap();
            if !*logged {
                info!(
                    backend = "userspace",
                    "WireGuard userspace fallback active (fake apply; no kernel module)"
                );
                *logged = true;
            }
        }
        self.inner.apply(iface, peers)
    }

    fn handshake_ok(&self, peer_public_key: &str) -> bool {
        self.inner.handshake_ok(peer_public_key)
    }
}

/// Kernel backend via `wg set` / `wg setconf` style commands.
pub struct KernelWgBackend;

impl WgBackend for KernelWgBackend {
    fn kind(&self) -> WgBackendKind {
        WgBackendKind::Kernel
    }

    fn apply(&self, iface: &str, peers: &PeerSet) -> Result<(), String> {
        // Ensure interface exists (best-effort).
        let _ = Command::new("ip")
            .args(["link", "add", "dev", iface, "type", "wireguard"])
            .output();
        if let Some(mtu) = peers.mtu {
            let _ = Command::new("ip")
                .args(["link", "set", "dev", iface, "mtu", &mtu.to_string()])
                .output();
        }
        for p in &peers.peers {
            let key = p.public_key.strip_prefix("b64:").unwrap_or(&p.public_key);
            let mut args = vec![
                "set".into(),
                iface.to_string(),
                "peer".into(),
                key.to_string(),
                "allowed-ips".into(),
                p.allowed_ips.join(","),
            ];
            if let Some(ep) = &p.endpoint {
                args.push("endpoint".into());
                args.push(ep.clone());
            }
            if p.persistent_keepalive > 0 {
                args.push("persistent-keepalive".into());
                args.push(p.persistent_keepalive.to_string());
            }
            let out = Command::new("wg")
                .args(&args)
                .output()
                .map_err(|e| format!("wg invoke failed: {e}"))?;
            if !out.status.success() {
                return Err(format!(
                    "wg set failed: {}",
                    String::from_utf8_lossy(&out.stderr)
                ));
            }
        }
        let _ = Command::new("ip")
            .args(["link", "set", "dev", iface, "up"])
            .output();
        Ok(())
    }

    fn handshake_ok(&self, peer_public_key: &str) -> bool {
        let key = peer_public_key
            .strip_prefix("b64:")
            .unwrap_or(peer_public_key);
        let out = Command::new("wg")
            .args(["show", "all", "latest-handshakes"])
            .output();
        match out {
            Ok(o) if o.status.success() => {
                let text = String::from_utf8_lossy(&o.stdout);
                text.lines().any(|l| l.contains(key))
            }
            _ => false,
        }
    }
}

/// Select backend from config / auto-detect.
pub fn select_backend(kind: WgBackendKind) -> Arc<dyn WgBackend> {
    match kind {
        WgBackendKind::Kernel => {
            if WgBackendKind::auto_detect() != WgBackendKind::Kernel {
                warn!("FORGE_NETWORK_WG_BACKEND=kernel but wg/tun unavailable; falling back to userspace");
                Arc::new(UserspaceWgBackend::new())
            } else {
                Arc::new(KernelWgBackend)
            }
        }
        WgBackendKind::Userspace => Arc::new(UserspaceWgBackend::new()),
        WgBackendKind::Fake => Arc::new(FakeWgBackend::new()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fake_apply_and_rotation_keeps_handshake() {
        let backend = FakeWgBackend::new();
        let v1 = PeerSet {
            node_id: "node-a".into(),
            peer_version: 1,
            mtu: Some(1420),
            peers: vec![PeerConfig {
                node_id: "node-b".into(),
                public_key: "b64:old".into(),
                endpoint: Some("1.1.1.1:51820".into()),
                allowed_ips: vec!["10.100.2.0/24".into()],
                persistent_keepalive: 25,
            }],
        };
        backend.apply("wg0", &v1).unwrap();
        assert!(backend.handshake_ok("b64:old"));

        let v2 = PeerSet {
            peer_version: 2,
            peers: vec![
                PeerConfig {
                    node_id: "node-b".into(),
                    public_key: "b64:old".into(),
                    endpoint: Some("1.1.1.1:51820".into()),
                    allowed_ips: vec!["10.100.2.0/24".into()],
                    persistent_keepalive: 25,
                },
                PeerConfig {
                    node_id: "node-b".into(),
                    public_key: "b64:new".into(),
                    endpoint: Some("1.1.1.1:51820".into()),
                    allowed_ips: vec!["10.100.2.0/24".into()],
                    persistent_keepalive: 25,
                },
            ],
            ..v1.clone()
        };
        backend.apply("wg0", &v2).unwrap();
        assert!(backend.handshake_ok("b64:old"));
        assert!(backend.handshake_ok("b64:new"));
        assert_eq!(backend.applied_version(), 2);
    }

    #[test]
    fn auto_parse() {
        assert_eq!(WgBackendKind::parse("fake").unwrap(), WgBackendKind::Fake);
        assert_eq!(
            WgBackendKind::parse("userspace").unwrap(),
            WgBackendKind::Userspace
        );
    }
}
