mod local;

pub use local::LocalStore;

use async_trait::async_trait;
use std::path::Path;

/// Seam for memory persistence roots. Local filesystem is the only implementation in 17.01.
#[async_trait]
pub trait Store: Send + Sync {
    /// Ensure the root exists, is under the allowed base, is not world-writable,
    /// and contains `vectors/` + `meta/` subtrees.
    async fn init(&self) -> Result<(), StoreError>;

    /// True when the root is present and a probe write/delete succeeds.
    async fn is_writable(&self) -> bool;

    fn root(&self) -> &Path;
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum StoreError {
    /// Fatal configuration / security violation — process must not start.
    Fatal(String),
    /// Transient or environmental failure — serve liveness, keep readiness 503.
    Unavailable(String),
}

impl std::fmt::Display for StoreError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Fatal(msg) | Self::Unavailable(msg) => write!(f, "{msg}"),
        }
    }
}

impl std::error::Error for StoreError {}
