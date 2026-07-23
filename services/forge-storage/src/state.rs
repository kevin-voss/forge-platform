use crate::backend::{BackendError, LocalFsBackend, StorageBackend};
use crate::config::Config;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tracing::{error, info, warn};

/// Shared application state for health and identity handlers.
#[derive(Clone)]
pub struct AppState {
    pub service_name: String,
    pub service_version: String,
    pub started_at: Instant,
    pub backend: Arc<LocalFsBackend>,
    pub ready: Arc<AtomicBool>,
}

impl AppState {
    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Relaxed)
    }

    pub async fn refresh_ready(&self) {
        let ok = self.backend.is_writable().await;
        let was = self.ready.swap(ok, Ordering::Relaxed);
        if ok && !was {
            info!(
                storage_root = %self.backend.root().display(),
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

/// Bootstrap the local FS backend. Fatal security/config errors abort startup;
/// transient unwritable roots keep the process up with readiness 503 + retry.
pub async fn bootstrap(cfg: &Config) -> Result<AppState, String> {
    let backend = Arc::new(LocalFsBackend::new(
        cfg.storage_root.clone(),
        cfg.allowed_base.clone(),
    ));

    info!(
        storage_root = %cfg.storage_root.display(),
        allowed_base = %cfg.allowed_base.display(),
        "initializing local filesystem storage backend"
    );

    match backend.init().await {
        Ok(()) => {
            let state = AppState {
                service_name: cfg.service_name.clone(),
                service_version: cfg.service_version.clone(),
                started_at: Instant::now(),
                backend,
                ready: Arc::new(AtomicBool::new(true)),
            };
            info!(
                storage_root = %state.backend.root().display(),
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
            })
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
