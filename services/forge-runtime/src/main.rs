mod config;
mod docker;
mod health;

use config::Config;
use docker::{startup_ping, BollardDocker};
use health::{router, AppState};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::signal;
use tracing::{error, info};
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        // Ensure fatal config/boot errors are visible even if tracing is not up.
        eprintln!(
            "{{\"level\":\"error\",\"service\":\"forge-runtime\",\"message\":\"fatal: {err}\"}}"
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
        auth_mode = %cfg.auth_mode,
        docker_host = %cfg.docker_host,
        shutdown_grace_seconds = cfg.shutdown_grace.as_secs(),
        "starting forge-runtime"
    );

    let docker = BollardDocker::connect(&cfg.docker_host);
    match startup_ping(
        &docker,
        cfg.docker_startup_retries,
        cfg.docker_startup_retry_delay,
    )
    .await
    {
        Ok(version) => {
            info!(
                service = %cfg.service_name,
                docker_engine_version = %version,
                "docker engine version recorded at startup"
            );
        }
        Err(err) => {
            // Do not exit: liveness remains available; readiness stays 503 until Docker recovers.
            error!(
                error = %err,
                "docker unreachable after startup retries; continuing with readiness=503"
            );
        }
    }

    let state = AppState {
        docker: Arc::new(docker),
    };
    let app = router(state);

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
    let filter = EnvFilter::try_new(format!(
        "info,forge_runtime={},bollard=warn,hyper=warn",
        cfg.log_level
    ))
    .unwrap_or_else(|_| EnvFilter::new("info"));

    // JSON logs with timestamp/level/fields; service is attached as a constant field.
    tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .with_current_span(false)
        .with_span_list(false)
        .flatten_event(true)
        .with_env_filter(filter)
        .with_writer(std::io::stdout)
        .init();

    // Emit a bootstrap line that always includes the `service` field expected by the platform.
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

    // Axum drains in-flight requests after this future completes; Compose stop_grace_period
    // must be >= FORGE_SHUTDOWN_GRACE_SECONDS. We do not sleep the full grace here so unit
    // and integration SIGTERM checks observe a prompt exit 0.
    let _ = grace;
}
