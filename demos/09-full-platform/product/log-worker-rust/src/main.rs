mod config;
mod log;
mod server;

use config::Config;
use log::Logger;
use serde_json::json;
use server::{router, AppState};
use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::Instant;
use tokio::signal;

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        eprintln!("fatal: {err}");
        std::process::exit(1);
    }
}

async fn run() -> Result<(), String> {
    let cfg = Config::from_env()?;
    let logger = Logger::new(&cfg.service_name, &cfg.log_level);
    let state = AppState {
        cfg: cfg.clone(),
        started_at: Instant::now(),
        entries: Arc::new(Mutex::new(Vec::new())),
    };
    let app = router(state);

    let addr = SocketAddr::from(([0, 0, 0, 0], cfg.port));
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .map_err(|e| format!("bind {addr}: {e}"))?;

    logger.info(
        "listening",
        &[
            ("port", json!(cfg.port)),
            ("version", json!(cfg.service_version)),
            ("env", json!(cfg.env)),
        ],
    );

    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal(logger.clone()))
        .await
        .map_err(|e| format!("serve: {e}"))?;

    logger.info("shutdown complete", &[]);
    Ok(())
}

async fn shutdown_signal(logger: Logger) {
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
            logger.info("shutdown signal received", &[("signal", json!("SIGINT"))]);
        }
        _ = terminate => {
            logger.info("shutdown signal received", &[("signal", json!("SIGTERM"))]);
        }
    }
}
