use super::{BackendError, PutStreamResult, StorageBackend};
use crate::integrity::{content_addressed_path, normalize_sha256};
use async_trait::async_trait;
use sha2::{Digest, Sha256};
use std::fs;
use std::io::Write;
use std::path::{Component, Path, PathBuf};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncSeekExt, AsyncWriteExt};
use tracing::{error, info};
use uuid::Uuid;

/// Default stream chunk size (64 KiB) when unset via config.
pub const DEFAULT_STREAM_BUFFER_BYTES: usize = 65_536;

/// Local filesystem backend rooted at `FORGE_STORAGE_ROOT`.
#[derive(Debug, Clone)]
pub struct LocalFsBackend {
    root: PathBuf,
    allowed_base: PathBuf,
}

impl LocalFsBackend {
    pub fn new(root: impl Into<PathBuf>, allowed_base: impl Into<PathBuf>) -> Self {
        Self {
            root: root.into(),
            allowed_base: allowed_base.into(),
        }
    }

    pub fn objects_dir(&self) -> PathBuf {
        self.root.join("objects")
    }

    pub fn meta_dir(&self) -> PathBuf {
        self.root.join("meta")
    }

    pub fn tmp_dir(&self) -> PathBuf {
        self.meta_dir().join("tmp")
    }

    /// Default SQLite metadata index path (`meta/index.db`).
    pub fn meta_db_path(&self) -> PathBuf {
        self.meta_dir().join("index.db")
    }

    /// Interim object layout (pre–content-addressing): `project/bucket/key-sha256`.
    pub fn interim_storage_path(
        project_id: &str,
        bucket_id: &str,
        key: &str,
    ) -> Result<String, BackendError> {
        sanitize_path_segment(project_id, "project_id")?;
        sanitize_path_segment(bucket_id, "bucket_id")?;
        // Key must already be validated by the API layer; still reject traversal here.
        if key.is_empty() || key.contains('\0') {
            return Err(BackendError::Io("invalid object key".into()));
        }
        if key.starts_with('/') || key.starts_with('\\') {
            return Err(BackendError::Io("object key must not be absolute".into()));
        }
        for seg in key.split(['/', '\\']) {
            if seg == ".." {
                return Err(BackendError::Io(
                    "object key must not contain '..' path segments".into(),
                ));
            }
        }
        let digest = Sha256::digest(key.as_bytes());
        let key_hash = hex::encode(digest);
        Ok(format!("{project_id}/{bucket_id}/{key_hash}"))
    }

    /// Resolve a relative `storage_path` under `objects/`, rejecting traversal.
    pub fn resolve_object_path(&self, storage_path: &str) -> Result<PathBuf, BackendError> {
        if storage_path.is_empty() || storage_path.contains('\0') {
            return Err(BackendError::Io("invalid storage_path".into()));
        }
        let rel = Path::new(storage_path);
        if rel.is_absolute() {
            return Err(BackendError::Io("storage_path must be relative".into()));
        }
        for comp in rel.components() {
            match comp {
                Component::Normal(_) => {}
                Component::CurDir => {}
                _ => {
                    return Err(BackendError::Io(
                        "storage_path must not contain '..' or prefixes".into(),
                    ));
                }
            }
        }
        let objects = normalize_path(&self.objects_dir());
        let dest = normalize_path(&objects.join(rel));
        if !path_is_under(&dest, &objects) {
            return Err(BackendError::Io(
                "storage_path escapes objects/ directory".into(),
            ));
        }
        Ok(dest)
    }

    /// Stream `reader` to a temp file while hashing, then place under content-addressed path.
    ///
    /// Uses a fixed-size buffer (`buffer_size`). On any failure the temp file is removed and
    /// the destination (if any) is left unchanged — readers never observe a partial object.
    /// When the blob already exists, the temp file is discarded (natural dedup).
    pub async fn put_stream_hashed<R: AsyncRead + Unpin>(
        &self,
        reader: &mut R,
        buffer_size: usize,
        max_bytes: Option<u64>,
        expected_sha256: Option<&str>,
    ) -> Result<PutStreamResult, BackendError> {
        let buffer_size = buffer_size.max(1);
        let tmp_dir = self.tmp_dir();
        tokio::fs::create_dir_all(&tmp_dir)
            .await
            .map_err(|e| BackendError::Io(format!("create tmp dir: {e}")))?;
        let tmp_path = tmp_dir.join(Uuid::new_v4().to_string());

        let result = self
            .write_temp_hash_and_place(&tmp_path, reader, buffer_size, max_bytes, expected_sha256)
            .await;

        if result.is_err() {
            let _ = tokio::fs::remove_file(&tmp_path).await;
        }
        result
    }

    /// Legacy helper used by unit tests: stream to an explicit relative `storage_path`.
    pub async fn put_stream<R: AsyncRead + Unpin>(
        &self,
        storage_path: &str,
        reader: &mut R,
        buffer_size: usize,
        max_bytes: Option<u64>,
    ) -> Result<u64, BackendError> {
        let buffer_size = buffer_size.max(1);
        let dest = self.resolve_object_path(storage_path)?;
        if let Some(parent) = dest.parent() {
            tokio::fs::create_dir_all(parent).await.map_err(|e| {
                BackendError::Io(format!("create object parent {}: {e}", parent.display()))
            })?;
        }

        let tmp_dir = self.tmp_dir();
        tokio::fs::create_dir_all(&tmp_dir)
            .await
            .map_err(|e| BackendError::Io(format!("create tmp dir: {e}")))?;
        let tmp_path = tmp_dir.join(Uuid::new_v4().to_string());

        let result = async {
            let (written, _hash) = self
                .write_temp_hashed(&tmp_path, reader, buffer_size, max_bytes)
                .await?;
            tokio::fs::rename(&tmp_path, &dest)
                .await
                .map_err(|e| BackendError::Io(format!("atomic rename: {e}")))?;
            if let Some(parent) = dest.parent() {
                if let Ok(dir) = tokio::fs::File::open(parent).await {
                    let _ = dir.sync_all().await;
                }
            }
            Ok(written)
        }
        .await;

        if result.is_err() {
            let _ = tokio::fs::remove_file(&tmp_path).await;
        }
        result
    }

    async fn write_temp_hash_and_place<R: AsyncRead + Unpin>(
        &self,
        tmp_path: &Path,
        reader: &mut R,
        buffer_size: usize,
        max_bytes: Option<u64>,
        expected_sha256: Option<&str>,
    ) -> Result<PutStreamResult, BackendError> {
        let (written, sha256) = self
            .write_temp_hashed(tmp_path, reader, buffer_size, max_bytes)
            .await?;

        if let Some(expected_raw) = expected_sha256 {
            let Some(expected) = normalize_sha256(expected_raw) else {
                return Err(BackendError::ChecksumMismatch {
                    expected: expected_raw.trim().to_string(),
                    actual: sha256,
                });
            };
            if expected != sha256 {
                return Err(BackendError::ChecksumMismatch {
                    expected,
                    actual: sha256,
                });
            }
        }

        let storage_path = content_addressed_path(&sha256)
            .map_err(|e| BackendError::Io(format!("content path: {e}")))?;
        let dest = self.resolve_object_path(&storage_path)?;
        if let Some(parent) = dest.parent() {
            tokio::fs::create_dir_all(parent).await.map_err(|e| {
                BackendError::Io(format!("create object parent {}: {e}", parent.display()))
            })?;
        }

        let dedup_hit = tokio::fs::try_exists(&dest)
            .await
            .map_err(|e| BackendError::Io(format!("stat dest: {e}")))?;
        if dedup_hit {
            let _ = tokio::fs::remove_file(tmp_path).await;
            info!(sha256 = %sha256, storage_path = %storage_path, "storage dedup hit");
        } else {
            tokio::fs::rename(tmp_path, &dest)
                .await
                .map_err(|e| BackendError::Io(format!("atomic rename: {e}")))?;
            if let Some(parent) = dest.parent() {
                if let Ok(dir) = tokio::fs::File::open(parent).await {
                    let _ = dir.sync_all().await;
                }
            }
            info!(sha256 = %sha256, storage_path = %storage_path, "storage dedup miss");
        }

        Ok(PutStreamResult {
            size_bytes: written,
            sha256,
            storage_path,
            dedup_hit,
        })
    }

    async fn write_temp_hashed<R: AsyncRead + Unpin>(
        &self,
        tmp_path: &Path,
        reader: &mut R,
        buffer_size: usize,
        max_bytes: Option<u64>,
    ) -> Result<(u64, String), BackendError> {
        let mut opts = tokio::fs::OpenOptions::new();
        opts.create_new(true).write(true);
        #[cfg(unix)]
        {
            opts.mode(0o600);
        }
        let mut file = opts.open(tmp_path).await.map_err(|e| {
            BackendError::Io(format!("create temp {}: {e}", tmp_path.display()))
        })?;

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let perms = fs::Permissions::from_mode(0o600);
            tokio::fs::set_permissions(tmp_path, perms)
                .await
                .map_err(|e| BackendError::Io(format!("chmod temp: {e}")))?;
        }

        let mut hasher = Sha256::new();
        let mut buf = vec![0u8; buffer_size];
        let mut written: u64 = 0;
        loop {
            let n = reader
                .read(&mut buf)
                .await
                .map_err(|e| BackendError::Io(format!("read upload stream: {e}")))?;
            if n == 0 {
                break;
            }
            written = written.saturating_add(n as u64);
            if let Some(max) = max_bytes {
                if written > max {
                    return Err(BackendError::TooLarge { max_bytes: max });
                }
            }
            hasher.update(&buf[..n]);
            file.write_all(&buf[..n])
                .await
                .map_err(|e| BackendError::Io(format!("write temp: {e}")))?;
        }

        file.flush()
            .await
            .map_err(|e| BackendError::Io(format!("flush temp: {e}")))?;
        file.sync_all()
            .await
            .map_err(|e| BackendError::Io(format!("fsync temp: {e}")))?;
        drop(file);

        Ok((written, hex::encode(hasher.finalize())))
    }

    /// Open a stored object for streamed download.
    pub async fn open_object(&self, storage_path: &str) -> Result<(tokio::fs::File, u64), BackendError> {
        let path = self.resolve_object_path(storage_path)?;
        let meta = tokio::fs::metadata(&path).await.map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                BackendError::NotFound(format!("object missing at {}", path.display()))
            } else {
                BackendError::Io(format!("stat {}: {e}", path.display()))
            }
        })?;
        let file = tokio::fs::File::open(&path).await.map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                BackendError::NotFound(format!("object missing at {}", path.display()))
            } else {
                BackendError::Io(format!("open {}: {e}", path.display()))
            }
        })?;
        Ok((file, meta.len()))
    }

    /// Open a stored object positioned at `start` for a ranged download of `length` bytes.
    pub async fn open_object_range(
        &self,
        storage_path: &str,
        start: u64,
        length: u64,
    ) -> Result<(tokio::fs::File, u64), BackendError> {
        let (mut file, len) = self.open_object(storage_path).await?;
        if start >= len || length == 0 || start.saturating_add(length) > len {
            return Err(BackendError::Io(format!(
                "range out of bounds: start={start} length={length} size={len}"
            )));
        }
        file.seek(std::io::SeekFrom::Start(start))
            .await
            .map_err(|e| BackendError::Io(format!("seek: {e}")))?;
        Ok((file, length))
    }

    /// Re-hash the on-disk blob and compare to `expected_sha256`.
    pub async fn verify_object_sha256(
        &self,
        storage_path: &str,
        expected_sha256: &str,
        buffer_size: usize,
    ) -> Result<(), BackendError> {
        let expected = normalize_sha256(expected_sha256).ok_or_else(|| {
            BackendError::Integrity(format!("invalid expected sha256 {expected_sha256:?}"))
        })?;
        let (mut file, _len) = self.open_object(storage_path).await?;
        let mut hasher = Sha256::new();
        let mut buf = vec![0u8; buffer_size.max(1)];
        loop {
            let n = file
                .read(&mut buf)
                .await
                .map_err(|e| BackendError::Io(format!("read for verify: {e}")))?;
            if n == 0 {
                break;
            }
            hasher.update(&buf[..n]);
        }
        let actual = hex::encode(hasher.finalize());
        if actual != expected {
            return Err(BackendError::Integrity(format!(
                "on-disk sha256 mismatch: expected {expected}, got {actual}"
            )));
        }
        Ok(())
    }

    /// Absolute filesystem path for a relative `storage_path` (tests / corruption helpers).
    pub fn absolute_object_path(&self, storage_path: &str) -> Result<PathBuf, BackendError> {
        self.resolve_object_path(storage_path)
    }

    /// Count files currently in `meta/tmp` (for tests / cleanup verification).
    pub fn count_tmp_files(&self) -> Result<usize, BackendError> {
        let tmp = self.tmp_dir();
        if !tmp.is_dir() {
            return Ok(0);
        }
        let mut n = 0;
        for entry in fs::read_dir(&tmp)
            .map_err(|e| BackendError::Io(format!("read tmp: {e}")))?
        {
            let entry = entry.map_err(|e| BackendError::Io(format!("tmp entry: {e}")))?;
            if entry.file_type().map(|t| t.is_file()).unwrap_or(false) {
                n += 1;
            }
        }
        Ok(n)
    }

    fn resolve_under_base(&self) -> Result<PathBuf, BackendError> {
        let base = normalize_path(&self.allowed_base);
        let root = normalize_path(&self.root);

        if !path_is_under(&root, &base) {
            return Err(BackendError::Fatal(format!(
                "FORGE_STORAGE_ROOT ({}) resolves outside allowed base ({})",
                root.display(),
                base.display()
            )));
        }
        Ok(root)
    }

    fn ensure_not_world_writable(path: &Path) -> Result<(), BackendError> {
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let meta = fs::metadata(path)
                .map_err(|e| BackendError::Unavailable(format!("stat {}: {e}", path.display())))?;
            let mode = meta.permissions().mode();
            if mode & 0o002 != 0 {
                return Err(BackendError::Fatal(format!(
                    "FORGE_STORAGE_ROOT ({}) is world-writable (mode {:o})",
                    path.display(),
                    mode & 0o777
                )));
            }
        }
        let _ = path;
        Ok(())
    }

    fn probe_writable(root: &Path) -> Result<(), BackendError> {
        let probe = root.join(".forge-storage-write-probe");
        {
            let mut f = fs::OpenOptions::new()
                .create(true)
                .write(true)
                .truncate(true)
                .open(&probe)
                .map_err(|e| {
                    BackendError::Unavailable(format!(
                        "FORGE_STORAGE_ROOT ({}) is not writable: {e}",
                        root.display()
                    ))
                })?;
            f.write_all(b"ok").map_err(|e| {
                BackendError::Unavailable(format!(
                    "FORGE_STORAGE_ROOT ({}) is not writable: {e}",
                    root.display()
                ))
            })?;
        }
        fs::remove_file(&probe).map_err(|e| {
            BackendError::Unavailable(format!(
                "FORGE_STORAGE_ROOT ({}) cleanup failed: {e}",
                root.display()
            ))
        })?;
        Ok(())
    }
}

fn sanitize_path_segment(value: &str, label: &str) -> Result<(), BackendError> {
    if value.is_empty() || value.contains('\0') {
        return Err(BackendError::Io(format!("invalid {label}")));
    }
    if value.contains('/') || value.contains('\\') || value.contains("..") {
        return Err(BackendError::Io(format!(
            "{label} must not contain path separators or '..'"
        )));
    }
    Ok(())
}

#[async_trait]
impl StorageBackend for LocalFsBackend {
    async fn init(&self) -> Result<(), BackendError> {
        let root = self.resolve_under_base()?;

        fs::create_dir_all(&root).map_err(|e| {
            BackendError::Unavailable(format!(
                "create FORGE_STORAGE_ROOT ({}): {e}",
                root.display()
            ))
        })?;

        let objects = root.join("objects");
        let meta = root.join("meta");
        let tmp = meta.join("tmp");
        fs::create_dir_all(&objects)
            .map_err(|e| BackendError::Unavailable(format!("create objects/: {e}")))?;
        fs::create_dir_all(&meta)
            .map_err(|e| BackendError::Unavailable(format!("create meta/: {e}")))?;
        fs::create_dir_all(&tmp)
            .map_err(|e| BackendError::Unavailable(format!("create meta/tmp/: {e}")))?;

        // Probe first so read-only mounts (often mode 777) stay Unavailable / 503
        // rather than fatal. World-writable is refused only when the root is usable.
        Self::probe_writable(&root)?;
        Self::ensure_not_world_writable(&root)?;

        info!(
            storage_root = %root.display(),
            objects = %objects.display(),
            meta = %meta.display(),
            "local filesystem storage root ready"
        );
        Ok(())
    }

    async fn is_writable(&self) -> bool {
        match self.resolve_under_base() {
            Ok(root) => {
                if !root.is_dir() {
                    return false;
                }
                if !self.objects_dir().is_dir() || !self.meta_dir().is_dir() {
                    return false;
                }
                if let Err(err) = Self::ensure_not_world_writable(&root) {
                    error!(error = %err, "storage root failed security check");
                    return false;
                }
                match Self::probe_writable(&root) {
                    Ok(()) => true,
                    Err(err) => {
                        error!(error = %err, "storage root not writable");
                        false
                    }
                }
            }
            Err(err) => {
                error!(error = %err, "storage root path invalid");
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
    use std::io::Cursor;
    use tempfile::tempdir;

    #[tokio::test]
    async fn init_creates_objects_meta_and_tmp() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("storage");
        let backend = LocalFsBackend::new(&root, dir.path());
        backend.init().await.expect("init");
        assert!(root.join("objects").is_dir());
        assert!(root.join("meta").is_dir());
        assert!(root.join("meta").join("tmp").is_dir());
        assert!(backend.is_writable().await);
    }

    #[tokio::test]
    async fn init_errors_on_unwritable_root() {
        let dir = tempdir().unwrap();
        // Parent path is a regular file → mkdir fails for any uid (including root in CI).
        let blocker = dir.path().join("not-a-directory");
        fs::write(&blocker, b"x").unwrap();
        let root = blocker.join("storage");
        let backend = LocalFsBackend::new(&root, dir.path());
        let err = backend.init().await.expect_err("unwritable");
        assert!(matches!(err, BackendError::Unavailable(_)));
    }

    #[tokio::test]
    async fn refuses_root_outside_allowed_base() {
        let dir = tempdir().unwrap();
        let outside = tempdir().unwrap();
        let backend = LocalFsBackend::new(outside.path(), dir.path());
        let err = backend.init().await.expect_err("outside base");
        assert!(matches!(err, BackendError::Fatal(_)));
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
        let backend = LocalFsBackend::new(&root, dir.path());
        let err = backend.init().await.expect_err("world-writable");
        assert!(matches!(err, BackendError::Fatal(_)));
        assert!(err.to_string().contains("world-writable"));
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = fs::metadata(&root).unwrap().permissions();
            perms.set_mode(0o755);
            fs::set_permissions(&root, perms).unwrap();
        }
    }

    #[test]
    fn key_sanitization_maps_safe_keys_rejects_traversal() {
        let path = LocalFsBackend::interim_storage_path("proj-a", "bucket-id", "path/to/obj.txt")
            .expect("safe");
        assert!(path.starts_with("proj-a/bucket-id/"));
        assert_eq!(path.matches('/').count(), 2);
        assert!(LocalFsBackend::interim_storage_path("proj-a", "b", "../secret").is_err());
        assert!(LocalFsBackend::interim_storage_path("proj/a", "b", "ok").is_err());
        assert!(LocalFsBackend::interim_storage_path("proj-a", "b/c", "ok").is_err());
    }

    #[tokio::test]
    async fn put_stream_round_trip_and_buffer_chunks() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("storage");
        let backend = LocalFsBackend::new(&root, dir.path());
        backend.init().await.expect("init");

        let storage_path = LocalFsBackend::interim_storage_path("p", "b", "k.bin").unwrap();
        let payload: Vec<u8> = (0..200_000u32).map(|i| (i % 251) as u8).collect();
        let buffer_size = 4096;
        let mut cursor = Cursor::new(payload.clone());
        let written = backend
            .put_stream(&storage_path, &mut cursor, buffer_size, None)
            .await
            .expect("put");
        assert_eq!(written, payload.len() as u64);
        assert_eq!(backend.count_tmp_files().unwrap(), 0);

        let (mut file, len) = backend.open_object(&storage_path).await.expect("open");
        assert_eq!(len, payload.len() as u64);
        let mut out = Vec::new();
        let mut buf = vec![0u8; buffer_size];
        loop {
            let n = file.read(&mut buf).await.unwrap();
            if n == 0 {
                break;
            }
            assert!(n <= buffer_size, "chunk exceeded buffer");
            out.extend_from_slice(&buf[..n]);
        }
        assert_eq!(out, payload);
    }

    #[tokio::test]
    async fn atomic_rename_leaves_no_partial_on_write_failure() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("storage");
        let backend = LocalFsBackend::new(&root, dir.path());
        backend.init().await.expect("init");

        let storage_path = LocalFsBackend::interim_storage_path("p", "b", "fail.bin").unwrap();
        let dest = backend.resolve_object_path(&storage_path).unwrap();

        // Failing reader after some bytes.
        struct FailAfter {
            left: usize,
            failed: bool,
        }
        impl AsyncRead for FailAfter {
            fn poll_read(
                mut self: std::pin::Pin<&mut Self>,
                _cx: &mut std::task::Context<'_>,
                buf: &mut tokio::io::ReadBuf<'_>,
            ) -> std::task::Poll<std::io::Result<()>> {
                if self.failed {
                    return std::task::Poll::Ready(Err(std::io::Error::new(
                        std::io::ErrorKind::BrokenPipe,
                        "simulated disconnect",
                    )));
                }
                if self.left == 0 {
                    self.failed = true;
                    return std::task::Poll::Ready(Err(std::io::Error::new(
                        std::io::ErrorKind::BrokenPipe,
                        "simulated disconnect",
                    )));
                }
                let n = buf.remaining().min(self.left).min(1024);
                let zeros = vec![0u8; n];
                buf.put_slice(&zeros);
                self.left -= n;
                std::task::Poll::Ready(Ok(()))
            }
        }

        let mut reader = FailAfter {
            left: 8_192,
            failed: false,
        };
        let err = backend
            .put_stream(&storage_path, &mut reader, 1024, None)
            .await
            .expect_err("should fail");
        assert!(matches!(err, BackendError::Io(_)));
        assert!(!dest.exists(), "partial object must not be visible");
        assert_eq!(backend.count_tmp_files().unwrap(), 0);
    }

    #[tokio::test]
    async fn put_stream_honors_max_bytes() {
        let dir = tempdir().unwrap();
        let root = dir.path().join("storage");
        let backend = LocalFsBackend::new(&root, dir.path());
        backend.init().await.expect("init");
        let storage_path = LocalFsBackend::interim_storage_path("p", "b", "big.bin").unwrap();
        let payload = vec![7u8; 10_000];
        let mut cursor = Cursor::new(payload);
        let err = backend
            .put_stream(&storage_path, &mut cursor, 1024, Some(1000))
            .await
            .expect_err("too large");
        assert!(matches!(err, BackendError::TooLarge { .. }));
        assert!(!backend.resolve_object_path(&storage_path).unwrap().exists());
        assert_eq!(backend.count_tmp_files().unwrap(), 0);
    }
}
