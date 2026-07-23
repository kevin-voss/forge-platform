use crate::backend::{BackendError, LocalFsBackend, StorageBackend};
use crate::config::{AuthMode, Config, VerifyOnRead};
use crate::identity::{HttpIdentityClient, IdentityClient};
use crate::meta::MetadataStore;
use crate::signing::{system_clock, Clock, SigningKeys};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tracing::{error, info, warn};

/// Counters for storage observability (13.02–13.06).
#[derive(Debug, Default)]
pub struct StorageMetrics {
    pub buckets_created: AtomicU64,
    pub storage_buckets_total: AtomicU64,
    pub storage_objects_total: AtomicU64,
    pub storage_upload_bytes_total: AtomicU64,
    pub storage_download_bytes_total: AtomicU64,
    pub storage_uploads_total: AtomicU64,
    pub storage_downloads_total: AtomicU64,
    pub storage_dedup_hits_total: AtomicU64,
    pub storage_integrity_errors_total: AtomicU64,
    pub storage_range_requests_total: AtomicU64,
    pub storage_tokens_issued_total: AtomicU64,
    pub storage_token_rejections_total: AtomicU64,
    pub storage_quota_rejections_total: AtomicU64,
    pub storage_blobs_gc_total: AtomicU64,
    pub storage_used_bytes: AtomicU64,
}

impl StorageMetrics {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }
}

/// Shared application state for health, identity, and bucket handlers.
#[derive(Clone)]
pub struct AppState {
    pub service_name: String,
    pub service_version: String,
    pub started_at: Instant,
    pub backend: Arc<LocalFsBackend>,
    pub ready: Arc<AtomicBool>,
    pub meta: Option<Arc<MetadataStore>>,
    pub auth_mode: AuthMode,
    pub identity: Option<Arc<dyn IdentityClient>>,
    pub metrics: Arc<StorageMetrics>,
    pub meta_path: PathBuf,
    pub stream_buffer_bytes: usize,
    pub max_object_bytes: Option<u64>,
    pub verify_on_read: VerifyOnRead,
    pub signing: Option<Arc<SigningKeys>>,
    pub clock: Clock,
    /// Default per-project quota when no override row exists.
    pub default_quota_bytes: u64,
}

impl AppState {
    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Relaxed)
    }

    pub async fn refresh_ready(&self) {
        let ok = self.backend.is_writable().await && self.meta.is_some();
        let was = self.ready.swap(ok, Ordering::Relaxed);
        if ok && !was {
            info!(
                storage_root = %self.backend.root().display(),
                meta_path = %self.meta_path.display(),
                "backend readiness transition: ready"
            );
        } else if !ok && was {
            warn!(
                storage_root = %self.backend.root().display(),
                "backend readiness transition: not_ready"
            );
        }
    }
}

fn signing_from_config(cfg: &Config) -> Option<Arc<SigningKeys>> {
    let key = cfg.signing_key.clone()?;
    Some(Arc::new(SigningKeys {
        key,
        key_prev: cfg.signing_key_prev.clone(),
        max_ttl_seconds: cfg.max_ttl_seconds,
        clock_skew_seconds: cfg.clock_skew_seconds,
    }))
}

/// Bootstrap the local FS backend and metadata SQLite index.
pub async fn bootstrap(cfg: &Config) -> Result<AppState, String> {
    let backend = Arc::new(LocalFsBackend::new(
        cfg.storage_root.clone(),
        cfg.allowed_base.clone(),
    ));

    info!(
        storage_root = %cfg.storage_root.display(),
        allowed_base = %cfg.allowed_base.display(),
        meta_path = %cfg.meta_path.display(),
        auth_mode = %cfg.auth_mode,
        "initializing local filesystem storage backend"
    );

    let identity: Option<Arc<dyn IdentityClient>> = match cfg.auth_mode {
        AuthMode::Enforce => {
            let url = cfg.identity_url.as_deref().ok_or_else(|| {
                "FORGE_IDENTITY_URL is required when FORGE_AUTH_MODE=enforce".to_string()
            })?;
            Some(HttpIdentityClient::new(url, cfg.identity_cache_ttl_secs)?
                as Arc<dyn IdentityClient>)
        }
        AuthMode::Dev => {
            warn!("FORGE_AUTH_MODE=dev — project isolation via X-Forge-Project only (insecure)");
            None
        }
    };

    let signing = signing_from_config(cfg);
    if signing.is_none() {
        warn!("FORGE_STORAGE_SIGNING_KEY unset — signed token issue/verify disabled");
    }

    let metrics = StorageMetrics::new();
    let clock = system_clock();

    match backend.init().await {
        Ok(()) => {
            let meta = MetadataStore::open(&cfg.meta_path)
                .map_err(|e| format!("open metadata store: {e}"))?;
            info!(
                meta_path = %cfg.meta_path.display(),
                "metadata SQLite index ready"
            );

            if cfg.reconcile_on_boot {
                match meta.reconcile() {
                    Ok(report) => {
                        let keep = meta.live_blob_paths().unwrap_or_default();
                        let orphans_from_meta = report.orphan_blob_paths.len();
                        let gc = backend
                            .gc_orphan_blobs(&keep)
                            .await
                            .map_err(|e| format!("boot orphan GC: {e}"))?;
                        metrics
                            .storage_blobs_gc_total
                            .fetch_add(gc, Ordering::Relaxed);
                        info!(
                            projects = report.projects,
                            blobs = report.blobs,
                            orphan_paths_from_meta = orphans_from_meta,
                            orphan_files_gc = gc,
                            "boot reconcile complete"
                        );
                    }
                    Err(e) => {
                        warn!(error = %e, "boot reconcile failed; continuing with existing counters");
                    }
                }
            }

            let state = AppState {
                service_name: cfg.service_name.clone(),
                service_version: cfg.service_version.clone(),
                started_at: Instant::now(),
                backend,
                ready: Arc::new(AtomicBool::new(true)),
                meta: Some(Arc::new(meta)),
                auth_mode: cfg.auth_mode,
                identity,
                metrics,
                meta_path: cfg.meta_path.clone(),
                stream_buffer_bytes: cfg.stream_buffer_bytes,
                max_object_bytes: cfg.max_object_bytes,
                verify_on_read: cfg.verify_on_read,
                signing,
                clock,
                default_quota_bytes: cfg.default_quota_bytes,
            };
            info!(
                storage_root = %state.backend.root().display(),
                default_quota_bytes = state.default_quota_bytes,
                "backend readiness transition: ready"
            );
            Ok(state)
        }
        Err(BackendError::Fatal(msg)) => {
            error!(error = %msg, "fatal storage backend configuration");
            Err(msg)
        }
        Err(BackendError::Unavailable(msg)) => {
            warn!(
                error = %msg,
                storage_root = %cfg.storage_root.display(),
                "storage root not ready at boot; continuing with readiness=503"
            );
            Ok(AppState {
                service_name: cfg.service_name.clone(),
                service_version: cfg.service_version.clone(),
                started_at: Instant::now(),
                backend,
                ready: Arc::new(AtomicBool::new(false)),
                meta: None,
                auth_mode: cfg.auth_mode,
                identity,
                metrics,
                meta_path: cfg.meta_path.clone(),
                stream_buffer_bytes: cfg.stream_buffer_bytes,
                max_object_bytes: cfg.max_object_bytes,
                verify_on_read: cfg.verify_on_read,
                signing,
                clock,
                default_quota_bytes: cfg.default_quota_bytes,
            })
        }
        Err(err) => {
            // init() only returns Fatal/Unavailable; other variants are object-transfer errors.
            error!(error = %err, "unexpected storage backend init error");
            Err(err.to_string())
        }
    }
}

/// Background retry with exponential backoff until the root becomes writable.
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
            state.refresh_ready().await;
            if state.is_ready() {
                return;
            }
            tokio::time::sleep(delay).await;
            delay = (delay.saturating_mul(2)).min(max);
        }
    })
}
