mod local_fs;

pub use local_fs::{LocalFsBackend, DEFAULT_STREAM_BUFFER_BYTES};

use async_trait::async_trait;
use std::path::Path;

/// Seam for storage backends. Local filesystem is the only implementation in 13.01.
#[async_trait]
pub trait StorageBackend: Send + Sync {
    /// Ensure the root exists, is under the allowed base, is not world-writable,
    /// and contains `objects/` + `meta/` subtrees.
    async fn init(&self) -> Result<(), BackendError>;

    /// True when the root is present and a probe write/delete succeeds.
    async fn is_writable(&self) -> bool;

    fn root(&self) -> &Path;
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BackendError {
    /// Fatal configuration / security violation — process must not start.
    Fatal(String),
    /// Transient or environmental failure — serve liveness, keep readiness 503.
    Unavailable(String),
    /// Object / path not found.
    NotFound(String),
    /// I/O or streaming failure during object transfer.
    Io(String),
    /// Upload exceeded configured max object size.
    TooLarge { max_bytes: u64 },
    /// Client `X-Expected-SHA256` did not match the streamed content.
    ChecksumMismatch { expected: String, actual: String },
    /// On-disk blob failed SHA-256 verification.
    Integrity(String),
}

impl std::fmt::Display for BackendError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Fatal(msg)
            | Self::Unavailable(msg)
            | Self::NotFound(msg)
            | Self::Io(msg)
            | Self::Integrity(msg) => write!(f, "{msg}"),
            Self::TooLarge { max_bytes } => {
                write!(f, "object exceeds max size of {max_bytes} bytes")
            }
            Self::ChecksumMismatch { expected, actual } => {
                write!(f, "checksum mismatch: expected {expected}, got {actual}")
            }
        }
    }
}

/// Result of a content-addressed streamed upload.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PutStreamResult {
    pub size_bytes: u64,
    pub sha256: String,
    pub storage_path: String,
    pub dedup_hit: bool,
}

impl std::error::Error for BackendError {}
