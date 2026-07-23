use crate::config::Config;
use crate::store::{LocalStore, Store, StoreError};
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
    pub store: Arc<LocalStore>,
    pub ready: Arc<AtomicBool>,
}

impl AppState {
    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Relaxed)
    }

    pub async fn refresh_ready(&self) {
        let ok = self.store.is_writable().await;
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

/// Bootstrap the local FS store (vectors/ + meta/).
pub async fn bootstrap(cfg: &Config) -> Result<AppState, String> {
    let store = Arc::new(LocalStore::new(
        cfg.memory_root.clone(),
        cfg.allowed_base.clone(),
    ));

    info!(
        memory_root = %cfg.memory_root.display(),
        allowed_base = %cfg.allowed_base.display(),
        "initializing local filesystem memory store"
    );

    match store.init().await {
        Ok(()) => {
            let state = AppState {
                service_name: cfg.service_name.clone(),
                service_version: cfg.service_version.clone(),
                started_at: Instant::now(),
                store,
                ready: Arc::new(AtomicBool::new(true)),
            };
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
            Ok(AppState {
                service_name: cfg.service_name.clone(),
                service_version: cfg.service_version.clone(),
                started_at: Instant::now(),
                store,
                ready: Arc::new(AtomicBool::new(false)),
            })
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
