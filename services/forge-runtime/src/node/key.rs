//! Node WireGuard X25519 key pair (22.02).
//!
//! Private key is generated locally, persisted mode `0600`, and never logged or
//! included in API payloads. Only the public key (`b64:...`) is advertised.

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine;
use rand_core::OsRng;
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use tracing::info;
use x25519_dalek::{PublicKey, StaticSecret};

const PRIVATE_KEY_FILENAME: &str = "wireguard_private.key";
const PUBLIC_KEY_FILENAME: &str = "wireguard_public.key";

/// Public WireGuard key advertised to Control (`b64:<standard-base64>`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NodePublicKey(String);

impl NodePublicKey {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl std::fmt::Display for NodePublicKey {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

/// Load or generate an X25519 key pair under `key_dir` (default: runtime data dir).
pub fn load_or_create_keypair(key_dir: impl AsRef<Path>) -> Result<NodePublicKey, String> {
    let key_dir = key_dir.as_ref();
    fs::create_dir_all(key_dir).map_err(|e| {
        format!(
            "FORGE_NODE_KEY_DIR {} is not creatable: {e}",
            key_dir.display()
        )
    })?;

    let private_path = key_dir.join(PRIVATE_KEY_FILENAME);
    let public_path = key_dir.join(PUBLIC_KEY_FILENAME);

    if private_path.exists() {
        let secret = load_private_key(&private_path)?;
        let public = PublicKey::from(&secret);
        let encoded = encode_public(&public);
        // Refresh public key file if missing/stale (never rewrite private).
        if !public_path.exists() {
            write_public_key(&public_path, &encoded)?;
        }
        ensure_private_mode_0600(&private_path)?;
        info!(
            key_dir = %key_dir.display(),
            "loaded persisted node WireGuard key pair"
        );
        return Ok(NodePublicKey(encoded));
    }

    let secret = StaticSecret::random_from_rng(OsRng);
    let public = PublicKey::from(&secret);
    let public_encoded = encode_public(&public);
    write_private_key(&private_path, &secret)?;
    write_public_key(&public_path, &public_encoded)?;
    info!(
        key_dir = %key_dir.display(),
        "generated new node WireGuard key pair"
    );
    Ok(NodePublicKey(public_encoded))
}

fn encode_public(public: &PublicKey) -> String {
    format!("b64:{}", B64.encode(public.as_bytes()))
}

fn write_private_key(path: &Path, secret: &StaticSecret) -> Result<(), String> {
    let encoded = B64.encode(secret.to_bytes());
    write_mode_0600(path, encoded.as_bytes())?;
    Ok(())
}

fn write_public_key(path: &Path, encoded: &str) -> Result<(), String> {
    // Public key is not secret; still keep 0600 for consistency with node identity files.
    write_mode_0600(path, format!("{encoded}\n").as_bytes())?;
    Ok(())
}

fn load_private_key(path: &Path) -> Result<StaticSecret, String> {
    let raw = fs::read_to_string(path)
        .map_err(|e| format!("read private key {}: {e}", path.display()))?;
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err(format!("private key file {} is empty", path.display()));
    }
    let bytes = B64
        .decode(trimmed)
        .map_err(|e| format!("private key file {} is not valid base64: {e}", path.display()))?;
    if bytes.len() != 32 {
        return Err(format!(
            "private key file {} must decode to 32 bytes, got {}",
            path.display(),
            bytes.len()
        ));
    }
    let mut arr = [0u8; 32];
    arr.copy_from_slice(&bytes);
    Ok(StaticSecret::from(arr))
}

fn write_mode_0600(path: &Path, bytes: &[u8]) -> Result<(), String> {
    let mut opts = fs::OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    let mut file = opts
        .open(path)
        .map_err(|e| format!("create {}: {e}", path.display()))?;
    file.write_all(bytes)
        .map_err(|e| format!("write {}: {e}", path.display()))?;
    if !bytes.ends_with(b"\n") {
        file.write_all(b"\n")
            .map_err(|e| format!("write {}: {e}", path.display()))?;
    }
    file.sync_all()
        .map_err(|e| format!("sync {}: {e}", path.display()))?;
    ensure_private_mode_0600(path)?;
    Ok(())
}

fn ensure_private_mode_0600(path: &Path) -> Result<(), String> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let meta = fs::metadata(path)
            .map_err(|e| format!("stat {}: {e}", path.display()))?;
        let mode = meta.permissions().mode() & 0o777;
        if mode != 0o600 {
            let mut perms = meta.permissions();
            perms.set_mode(0o600);
            fs::set_permissions(path, perms)
                .map_err(|e| format!("chmod 0600 {}: {e}", path.display()))?;
        }
    }
    let _ = path;
    Ok(())
}

/// Resolve key directory: `FORGE_NODE_KEY_DIR` or fallback to runtime data dir.
pub fn resolve_key_dir(key_dir: Option<&Path>, data_dir: &Path) -> PathBuf {
    key_dir
        .map(Path::to_path_buf)
        .unwrap_or_else(|| data_dir.to_path_buf())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    use tempfile::tempdir;

    #[test]
    fn key_pair_generated_once_and_reloaded() {
        let dir = tempdir().unwrap();
        let pub1 = load_or_create_keypair(dir.path()).unwrap();
        let pub2 = load_or_create_keypair(dir.path()).unwrap();
        assert_eq!(pub1, pub2);
        assert!(pub1.as_str().starts_with("b64:"));

        let private_path = dir.path().join(PRIVATE_KEY_FILENAME);
        let meta = fs::metadata(&private_path).unwrap();
        assert_eq!(meta.permissions().mode() & 0o777, 0o600);

        // Private key material must never appear in the public key string.
        let private_raw = fs::read_to_string(&private_path).unwrap();
        let private_b64 = private_raw.trim();
        assert!(!pub1.as_str().contains(private_b64));
    }
}
