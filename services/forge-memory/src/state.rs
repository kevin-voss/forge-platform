use crate::collections::CollectionStore;
use crate::config::Config;
use crate::meta::MetaStore;
use crate::store::{LocalStore, Store, StoreError};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tracing::{error, info, warn};

/// Process-local counters (labelled Prometheus export later).
#[derive(Debug, Default)]
pub struct MemoryMetrics {
    pub memory_collections_total: AtomicU64,
    pub memory_records_total: AtomicU64,
}

/// Shared application state for health, identity, and collection APIs.
#[derive(Clone)]
pub struct AppState {
    pub service_name: String,
    pub service_version: String,
    pub started_at: Instant,
    pub store: Arc<LocalStore>,
    pub ready: Arc<AtomicBool>,
    pub collections: Arc<Mutex<Option<Arc<CollectionStore>>>>,
    pub metrics: Arc<MemoryMetrics>,
    pub list_page_size: usize,
    pub max_dim: usize,
    pub max_metadata_bytes: usize,
    pub meta_path: std::path::PathBuf,
}

impl AppState {
    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Relaxed)
    }

    /// Open CollectionStore when the FS root is ready and meta is not yet attached.
    pub fn ensure_collections(&self) -> Result<Arc<CollectionStore>, String> {
        let mut guard = self
            .collections
            .lock()
            .map_err(|_| "collections lock poisoned".to_string())?;
        if let Some(existing) = guard.as_ref() {
            return Ok(Arc::clone(existing));
        }
        let meta = MetaStore::open(&self.meta_path).map_err(|e| format!("open meta: {e}"))?;
        let collections = Arc::new(CollectionStore::new(
            Arc::new(meta),
            self.store.vectors_dir(),
            self.max_dim,
            self.max_metadata_bytes,
        ));
        *guard = Some(Arc::clone(&collections));
        info!(
            meta_path = %self.meta_path.display(),
            "metadata SQLite index ready"
        );
        Ok(collections)
    }

    pub async fn refresh_ready(&self) {
        let writable = self.store.is_writable().await;
        if writable {
            if let Err(e) = self.ensure_collections() {
                warn!(error = %e, "failed to attach collection store");
            }
        }
        let has_collections = self
            .collections
            .lock()
            .map(|g| g.is_some())
            .unwrap_or(false);
        let ok = writable && has_collections;
        let was = self.ready.swap(ok, Ordering::Relaxed);
        if ok && !was {
            info!(
                memory_root = %self.store.root().display(),
                "store readiness transition: ready"
            );
        } else if !ok && was {
            warn!(
                memory_root = %self.store.root().display(),
                "store readiness transition: not_ready"
            );
        }
    }
}

/// Bootstrap the local FS store, SQLite meta, and CollectionStore.
pub async fn bootstrap(cfg: &Config) -> Result<AppState, String> {
    let store = Arc::new(LocalStore::new(
        cfg.memory_root.clone(),
        cfg.allowed_base.clone(),
    ));
    let meta_path = store.meta_dir().join("index.db");

    info!(
        memory_root = %cfg.memory_root.display(),
        allowed_base = %cfg.allowed_base.display(),
        meta_path = %meta_path.display(),
        "initializing local filesystem memory store"
    );

    let state = AppState {
        service_name: cfg.service_name.clone(),
        service_version: cfg.service_version.clone(),
        started_at: Instant::now(),
        store: Arc::clone(&store),
        ready: Arc::new(AtomicBool::new(false)),
        collections: Arc::new(Mutex::new(None)),
        metrics: Arc::new(MemoryMetrics::default()),
        list_page_size: cfg.list_page_size,
        max_dim: cfg.max_dim,
        max_metadata_bytes: cfg.max_metadata_bytes,
        meta_path,
    };

    match store.init().await {
        Ok(()) => {
            state.ensure_collections()?;
            state.ready.store(true, Ordering::Relaxed);
            info!(
                memory_root = %state.store.root().display(),
                "store readiness transition: ready"
            );
            Ok(state)
        }
        Err(StoreError::Fatal(msg)) => {
            error!(error = %msg, "fatal memory store configuration");
            Err(msg)
        }
        Err(StoreError::Unavailable(msg)) => {
            warn!(
                error = %msg,
                memory_root = %cfg.memory_root.display(),
                "memory root not ready at boot; continuing with readiness=503"
            );
            Ok(state)
        }
    }
}

/// Periodically retry readiness when the store was unavailable at boot.
pub fn spawn_ready_retry(
    state: AppState,
    initial: Duration,
    max: Duration,
) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let mut delay = initial.max(Duration::from_millis(50));
        let max = max.max(delay);
        loop {
            if state.is_ready() {
                return;
            }
            tokio::time::sleep(delay).await;
            match state.store.init().await {
                Ok(()) => {
                    state.refresh_ready().await;
                    if state.is_ready() {
                        return;
                    }
                }
                Err(StoreError::Fatal(msg)) => {
                    error!(error = %msg, "ready retry hit fatal store error; stopping retries");
                    return;
                }
                Err(StoreError::Unavailable(msg)) => {
                    warn!(error = %msg, delay_ms = delay.as_millis(), "ready retry: still unavailable");
                }
            }
            delay = (delay * 2).min(max);
        }
    })
}
