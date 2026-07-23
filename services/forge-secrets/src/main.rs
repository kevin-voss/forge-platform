use forge_secrets::config::Config;
use forge_secrets::routes;
use forge_secrets::state::bootstrap;
use std::net::SocketAddr;
use tokio::signal;
use tracing::{info, warn};
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        eprintln!(
            "{{\"level\":\"error\",\"service\":\"forge-secrets\",\"message\":\"fatal: {err}\"}}"
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
        master_key_id = %cfg.master_key_id,
        master_key_configured = cfg.master_key_b64.is_some(),
        aead_alg = cfg.aead_alg.as_str(),
        max_value_bytes = cfg.max_value_bytes,
        shutdown_grace_seconds = cfg.shutdown_grace.as_secs(),
        "starting forge-secrets"
    );

    let state = bootstrap(&cfg).await;
    if !state.is_ready() {
        warn!(
            master_key_id = %state.master_key_id,
            crypto_ok = state.crypto_ok,
            error = state.crypto_error.as_deref().unwrap_or("db_or_migrate"),
            "service started not ready; secret/data-key ops refused until self-check + DB succeed"
        );
    }

    let app = axum::Router::new()
        .merge(routes::health::router())
        .merge(routes::identity::router())
        .merge(routes::data_keys::router())
        .merge(secrets_routes())
        .with_state(state);

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

fn secrets_routes() -> axum::Router<forge_secrets::state::AppState> {
    forge_secrets::secrets::routes::router()
}

fn init_tracing(cfg: &Config) {
    let filter = EnvFilter::try_new(format!(
        "info,forge_secrets={},sqlx=warn,hyper=warn",
        cfg.log_level
    ))
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
