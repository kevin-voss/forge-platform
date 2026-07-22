mod config;
mod docker;
mod health;
mod heartbeat;
mod node;
mod prober;
mod routes;
mod status;
mod workload;

use config::Config;
use docker::{startup_ping, BollardDocker};
use health::{router as health_router, AppState};
use heartbeat::Heartbeat;
use node::{maybe_register, Node};
use prober::{ProbeConfig, Prober, StatusCache};
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
        data_dir = %cfg.data_dir.display(),
        heartbeat_interval_seconds = cfg.heartbeat_interval.as_secs(),
        pull_timeout_seconds = cfg.pull_timeout.as_secs(),
        default_registry = %cfg.default_registry,
        probe_interval_seconds = cfg.probe_interval.as_secs(),
        probe_timeout_seconds = cfg.probe_timeout.as_secs(),
        probe_failure_threshold = cfg.probe_failure_threshold,
        probe_host = %cfg.probe_host,
        control_url = cfg.control_url.as_deref().unwrap_or(""),
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

    let node = Node::bootstrap(&cfg.data_dir, &docker).await?;
    let workload_labels = node.labels();
    info!(
        node_id = %node.info.id,
        label = %node::NODE_ID_LABEL,
        label_value = %workload_labels
            .get(node::NODE_ID_LABEL)
            .map(String::as_str)
            .unwrap_or(""),
        "node identity ready for workload labeling"
    );
    maybe_register(cfg.control_url.as_deref(), &node.info).await;

    let heartbeat = Arc::new(Heartbeat::new());
    let _heartbeat_task = heartbeat.spawn(cfg.heartbeat_interval);

    let docker = Arc::new(docker);
    let probe_cfg = ProbeConfig {
        interval: cfg.probe_interval,
        timeout: cfg.probe_timeout,
        failure_threshold: cfg.probe_failure_threshold,
        ready_path: cfg.probe_ready_path.clone(),
        live_path: cfg.probe_live_path.clone(),
        probe_host: cfg.probe_host.clone(),
    };
    let prober = Arc::new(Prober::new(
        Arc::clone(&docker) as Arc<dyn docker::DockerEngine>,
        Arc::new(StatusCache::new()),
        probe_cfg,
    )?);
    let _prober_task = prober.spawn();

    let state = AppState {
        docker,
        node: Arc::new(node),
        heartbeat,
        pull_timeout: cfg.pull_timeout,
        prober,
    };

    let app = axum::Router::new()
        .merge(health_router())
        .merge(routes::node::router())
        .merge(routes::workloads::router())
        .merge(routes::status::router())
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
