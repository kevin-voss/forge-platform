use forge_memory::app;
use forge_memory::config::Config;
use forge_memory::state::{bootstrap, spawn_ready_retry};
use std::net::SocketAddr;
use tokio::signal;
use tracing::{info, warn};
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        eprintln!(
            "{{\"level\":\"error\",\"service\":\"forge-memory\",\"message\":\"fatal: {err}\"}}"
        );
        std::process::exit(1);
    }
}

async fn run() -> Result<(), String> {
    let cfg = Config::from_env()?;
    init_tracing(&cfg);

    info!(
        service = %cfg.service_name,
        port = cfg.port,
        version = %cfg.service_version,
        env = %cfg.env,
        memory_root = %cfg.memory_root.display(),
        allowed_base = %cfg.allowed_base.display(),
        shutdown_grace_seconds = cfg.shutdown_grace.as_secs(),
        "starting forge-memory"
    );

    let state = bootstrap(&cfg).await?;
    if cfg.compact_on_boot {
        match state.ensure_collections() {
            Ok(collections) => match collections.compact_all() {
                Ok(removed) => {
                    info!(tombstones_removed = removed, "boot compaction complete");
                }
                Err(err) => {
                    warn!(error = %err, "boot compaction failed");
                }
            },
            Err(err) => {
                warn!(error = %err, "boot compaction skipped: collection store unavailable");
            }
        }
    }
    let _ready_retry =
        spawn_ready_retry(state.clone(), cfg.ready_retry_initial, cfg.ready_retry_max);

    let app = app(state);

    let addr = SocketAddr::from(([0, 0, 0, 0], cfg.port));
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .map_err(|e| format!("bind {addr}: {e}"))?;

    info!(%addr, "listening");

    let grace = cfg.shutdown_grace;
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal(grace))
        .await
        .map_err(|e| format!("serve: {e}"))?;

    info!("shutdown complete");
    Ok(())
}

fn init_tracing(cfg: &Config) {
    let filter = EnvFilter::try_new(format!("info,forge_memory={},hyper=warn", cfg.log_level))
        .unwrap_or_else(|_| EnvFilter::new("info"));

    tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .with_current_span(false)
        .with_span_list(false)
        .flatten_event(true)
        .with_env_filter(filter)
        .with_writer(std::io::stdout)
        .init();

    info!(
        service = %cfg.service_name,
        version = %cfg.service_version,
        "tracing initialized"
    );
}

async fn shutdown_signal(grace: std::time::Duration) {
    let ctrl_c = async {
        signal::ctrl_c()
            .await
            .expect("failed to install Ctrl+C handler");
    };

    #[cfg(unix)]
    let terminate = async {
        signal::unix::signal(signal::unix::SignalKind::terminate())
            .expect("failed to install SIGTERM handler")
            .recv()
            .await;
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {
            info!(signal = "SIGINT", grace_seconds = grace.as_secs(), "shutdown signal received");
        }
        _ = terminate => {
            info!(signal = "SIGTERM", grace_seconds = grace.as_secs(), "shutdown signal received");
        }
    }

    let _ = grace;
}
