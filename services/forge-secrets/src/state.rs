use crate::crypto::{
    generate_data_key, unwrap_data_key, wrap_data_key, EnvMasterKeyProvider, KeyProvider,
};
use crate::db::{self, ProjectDataKeyRow};
use sqlx::PgPool;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;
use tracing::{error, info, warn};

/// Shared application state for health and data-key bootstrap APIs.
#[derive(Clone)]
pub struct AppState {
    pub service_name: String,
    pub service_version: String,
    pub started_at: Instant,
    pub pool: Option<PgPool>,
    pub key_provider: Option<Arc<dyn KeyProvider>>,
    pub master_key_id: String,
    /// Gauge-like: 1 when ready (DB + valid master key + self-check).
    pub ready: Arc<AtomicBool>,
    /// Counter of persisted project data keys (best-effort).
    pub data_keys_total: Arc<AtomicU64>,
    pub crypto_ok: bool,
    pub crypto_error: Option<String>,
}

impl AppState {
    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Relaxed)
    }

    pub async fn refresh_ready(&self) {
        let ok = self.evaluate_ready().await;
        self.ready.store(ok, Ordering::Relaxed);
    }

    async fn evaluate_ready(&self) -> bool {
        if !self.crypto_ok || self.key_provider.is_none() {
            return false;
        }
        let Some(pool) = &self.pool else {
            return false;
        };
        db::ping(pool).await.is_ok()
    }

    /// Ensure a wrapped data key exists for `project_id`. Returns metadata only.
    pub async fn ensure_project_data_key(
        &self,
        project_id: &str,
    ) -> Result<(ProjectDataKeyRow, bool), EnsureError> {
        if !self.is_ready() {
            return Err(EnsureError::NotReady);
        }
        let pool = self.pool.as_ref().ok_or(EnsureError::NotReady)?;
        let provider = self.key_provider.as_ref().ok_or(EnsureError::NotReady)?;

        if let Some(existing) = db::get_project_data_key(pool, project_id)
            .await
            .map_err(EnsureError::Storage)?
        {
            // Prove unwrap still works without exposing plaintext.
            unwrap_data_key(provider.master_key(), &existing.wrapped_key)
                .map_err(EnsureError::Crypto)?;
            return Ok((existing, false));
        }

        let wrapped = {
            let data_key = generate_data_key();
            wrap_data_key(provider.master_key(), &data_key).map_err(EnsureError::Crypto)?
        };

        db::insert_project_data_key(pool, project_id, &wrapped, 1, provider.master_key_id())
            .await
            .map_err(EnsureError::Storage)?;

        self.data_keys_total.fetch_add(1, Ordering::Relaxed);
        let row = ProjectDataKeyRow {
            project_id: project_id.to_string(),
            wrapped_key: wrapped,
            key_version: 1,
            master_key_id: provider.master_key_id().to_string(),
        };
        Ok((row, true))
    }

    /// Load wrapped key and unwrap in memory (discard plaintext) to verify durability.
    pub async fn verify_project_data_key(
        &self,
        project_id: &str,
    ) -> Result<ProjectDataKeyRow, EnsureError> {
        if !self.is_ready() {
            return Err(EnsureError::NotReady);
        }
        let pool = self.pool.as_ref().ok_or(EnsureError::NotReady)?;
        let provider = self.key_provider.as_ref().ok_or(EnsureError::NotReady)?;
        let row = db::get_project_data_key(pool, project_id)
            .await
            .map_err(EnsureError::Storage)?
            .ok_or(EnsureError::NotFound)?;
        {
            let _plaintext = unwrap_data_key(provider.master_key(), &row.wrapped_key)
                .map_err(EnsureError::Crypto)?;
        }
        Ok(row)
    }
}

#[derive(Debug)]
pub enum EnsureError {
    NotReady,
    NotFound,
    Storage(String),
    Crypto(String),
}

pub async fn bootstrap(cfg: &crate::config::Config) -> AppState {
    let (key_provider, crypto_ok, crypto_error, master_key_id) =
        match EnvMasterKeyProvider::from_env() {
            Ok(provider) => {
                let master = *provider.master_key();
                let key_id = provider.master_key_id().to_string();
                match self_check_round_trip(&master) {
                    Ok(()) => {
                        info!(master_key_id = %key_id, ok = true, "master key self-check passed");
                        (Some(provider.into_arc()), true, None, key_id)
                    }
                    Err(err) => {
                        error!(
                            master_key_id = %key_id,
                            ok = false,
                            error = %err,
                            "master key self-check failed"
                        );
                        (None, false, Some(err), key_id)
                    }
                }
            }
            Err(err) => {
                warn!(
                    master_key_id = %cfg.master_key_id,
                    ok = false,
                    error = %err,
                    "master key self-check failed"
                );
                (None, false, Some(err), cfg.master_key_id.clone())
            }
        };

    let pool = match db::connect_with_retry(&cfg.db_url, 10, std::time::Duration::from_millis(500))
        .await
    {
        Ok(pool) => match db::migrate(&pool).await {
            Ok(()) => Some(pool),
            Err(err) => {
                error!(error = %err, "database migration failed");
                None
            }
        },
        Err(err) => {
            error!(error = %err, "database unavailable at startup; continuing with readiness=503");
            None
        }
    };

    let data_keys_total = Arc::new(AtomicU64::new(0));
    if let Some(pool) = &pool {
        if let Ok(count) = db::count_project_data_keys(pool).await {
            data_keys_total.store(count as u64, Ordering::Relaxed);
        }
    }

    let state = AppState {
        service_name: cfg.service_name.clone(),
        service_version: cfg.service_version.clone(),
        started_at: Instant::now(),
        pool,
        key_provider,
        master_key_id,
        ready: Arc::new(AtomicBool::new(false)),
        data_keys_total,
        crypto_ok,
        crypto_error,
    };
    state.refresh_ready().await;
    info!(
        forge_secrets_ready = state.is_ready() as u8,
        forge_data_keys_total = state.data_keys_total.load(Ordering::Relaxed),
        "readiness state initialized"
    );
    state
}

fn self_check_round_trip(master: &[u8; 32]) -> Result<(), String> {
    let key = generate_data_key();
    let wrapped = wrap_data_key(master, &key)?;
    let unwrapped = unwrap_data_key(master, &wrapped)?;
    if key.as_bytes() != unwrapped.as_bytes() {
        return Err("wrap/unwrap round-trip mismatch".into());
    }
    Ok(())
}
