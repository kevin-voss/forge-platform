use super::{Store, StoreError};
use async_trait::async_trait;
use std::fs;
use std::io::Write;
use std::path::{Component, Path, PathBuf};
use tracing::{error, info};

/// Local filesystem store rooted at `FORGE_MEMORY_ROOT`.
#[derive(Debug, Clone)]
pub struct LocalStore {
    root: PathBuf,
    allowed_base: PathBuf,
}

impl LocalStore {
    pub fn new(root: impl Into<PathBuf>, allowed_base: impl Into<PathBuf>) -> Self {
        Self {
            root: root.into(),
            allowed_base: allowed_base.into(),
        }
    }

    pub fn vectors_dir(&self) -> PathBuf {
        self.root.join("vectors")
    }

    pub fn meta_dir(&self) -> PathBuf {
        self.root.join("meta")
    }

    fn resolve_under_base(&self) -> Result<PathBuf, StoreError> {
        let root = normalize_path(&self.root);
        let base = normalize_path(&self.allowed_base);
        if !path_is_under(&root, &base) {
            return Err(StoreError::Fatal(format!(
                "FORGE_MEMORY_ROOT ({}) resolves outside allowed base ({})",
                root.display(),
                base.display()
            )));
        }
        Ok(root)
    }

    fn ensure_not_world_writable(path: &Path) -> Result<(), StoreError> {
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let meta = fs::metadata(path)
                .map_err(|e| StoreError::Unavailable(format!("stat {}: {e}", path.display())))?;
            let mode = meta.permissions().mode();
            if mode & 0o002 != 0 {
                return Err(StoreError::Fatal(format!(
                    "FORGE_MEMORY_ROOT ({}) is world-writable (mode {:o})",
                    path.display(),
                    mode & 0o777
                )));
            }
        }
        let _ = path;
        Ok(())
    }

    fn probe_writable(root: &Path) -> Result<(), StoreError> {
        let probe = root.join(".forge-memory-write-probe");
        {
            let mut f = fs::OpenOptions::new()
                .create(true)
                .write(true)
                .truncate(true)
                .open(&probe)
                .map_err(|e| {
                    StoreError::Unavailable(format!(
                        "FORGE_MEMORY_ROOT ({}) is not writable: {e}",
                        root.display()
                    ))
                })?;
            f.write_all(b"ok").map_err(|e| {
                StoreError::Unavailable(format!(
                    "FORGE_MEMORY_ROOT ({}) is not writable: {e}",
                    root.display()
                ))
            })?;
        }
        fs::remove_file(&probe).map_err(|e| {
            StoreError::Unavailable(format!(
                "FORGE_MEMORY_ROOT ({}) cleanup failed: {e}",
                root.display()
            ))
        })?;
        Ok(())
    }
}

#[async_trait]
impl Store for LocalStore {
    async fn init(&self) -> Result<(), StoreError> {
        let root = self.resolve_under_base()?;

        fs::create_dir_all(&root).map_err(|e| {
            StoreError::Unavailable(format!(
                "create FORGE_MEMORY_ROOT ({}): {e}",
                root.display()
            ))
        })?;

        let vectors = root.join("vectors");
        let meta = root.join("meta");
        fs::create_dir_all(&vectors)
            .map_err(|e| StoreError::Unavailable(format!("create vectors/: {e}")))?;
        fs::create_dir_all(&meta)
            .map_err(|e| StoreError::Unavailable(format!("create meta/: {e}")))?;

        // Probe first so read-only mounts (often mode 777) stay Unavailable / 503
        // rather than fatal. World-writable is refused only when the root is usable.
        Self::probe_writable(&root)?;
        Self::ensure_not_world_writable(&root)?;

        info!(
            memory_root = %root.display(),
            vectors = %vectors.display(),
            meta = %meta.display(),
            "local filesystem memory root ready"
        );
        Ok(())
    }

    async fn is_writable(&self) -> bool {
        match self.resolve_under_base() {
            Ok(root) => {
                if !root.is_dir() {
                    return false;
                }
                if !self.vectors_dir().is_dir() || !self.meta_dir().is_dir() {
                    return false;
                }
                if let Err(err) = Self::ensure_not_world_writable(&root) {
                    error!(error = %err, "memory root failed security check");
                    return false;
                }
                match Self::probe_writable(&root) {
                    Ok(()) => true,
                    Err(err) => {
                        error!(error = %err, "memory root not writable");
                        false
                    }
                }
            }
            Err(err) => {
                error!(error = %err, "memory root path invalid");
                false
            }
        }
    }

    fn root(&self) -> &Path {
        &self.root
    }
}

/// Lexical normalize (resolve `.` / `..`) without requiring the path to exist.
fn normalize_path(path: &Path) -> PathBuf {
    let mut out = PathBuf::new();
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        env_current_dir().join(path)
    };
    for comp in absolute.components() {
        match comp {
            Component::Prefix(p) => out.push(p.as_os_str()),
            Component::RootDir => out.push("/"),
            Component::CurDir => {}
            Component::ParentDir => {
                out.pop();
            }
            Component::Normal(c) => out.push(c),
        }
    }
    if out.as_os_str().is_empty() {
        PathBuf::from("/")
    } else {
        out
    }
}

fn env_current_dir() -> PathBuf {
    std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."))
}

fn path_is_under(path: &Path, base: &Path) -> bool {
    if path == base {
        return true;
    }
    path.starts_with(base)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[tokio::test]
    async fn init_creates_vectors_and_meta() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("memory");
        let store = LocalStore::new(&root, dir.path());
        store.init().await.expect("init");
        assert!(root.join("vectors").is_dir());
        assert!(root.join("meta").is_dir());
        assert!(store.is_writable().await);
    }

    #[tokio::test]
    async fn init_errors_on_unwritable_root() {
        let dir = tempdir().unwrap();
        // Parent path is a regular file → mkdir fails for any uid (including root in CI).
        let blocker = dir.path().join("not-a-directory");
        fs::write(&blocker, b"x").unwrap();
        let root = blocker.join("memory");
        let store = LocalStore::new(&root, dir.path());
        let err = store.init().await.expect_err("unwritable");
        assert!(matches!(err, StoreError::Unavailable(_)));
    }

    #[tokio::test]
    async fn refuses_root_outside_allowed_base() {
        let dir = tempdir().unwrap();
        let outside = tempdir().unwrap();
        let store = LocalStore::new(outside.path(), dir.path());
        let err = store.init().await.expect_err("outside base");
        assert!(matches!(err, StoreError::Fatal(_)));
        assert!(err.to_string().contains("outside allowed base"));
    }

    #[tokio::test]
    async fn refuses_world_writable_root() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("ww");
        fs::create_dir_all(&root).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = fs::metadata(&root).unwrap().permissions();
            perms.set_mode(0o777);
            fs::set_permissions(&root, perms).unwrap();
        }
        #[cfg(not(unix))]
        {
            return;
        }
        let store = LocalStore::new(&root, dir.path());
        let err = store.init().await.expect_err("world-writable");
        assert!(matches!(err, StoreError::Fatal(_)));
        assert!(err.to_string().contains("world-writable"));
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = fs::metadata(&root).unwrap().permissions();
            perms.set_mode(0o755);
            fs::set_permissions(&root, perms).unwrap();
        }
    }
}
