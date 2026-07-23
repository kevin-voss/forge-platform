mod config;
mod control_client;
mod converge;
mod discovery;
mod docker;
mod health;
mod heartbeat;
mod heartbeat_reporter;
mod lifecycle;
mod logs;
mod network;
mod node;
mod observability;
mod prober;
mod routes;
mod status;
mod workload;

use config::Config;
use control_client::ControlClient;
use converge::{spawn_reconciler, ReconcilerConfig};
use discovery::DiscoveryClient;
use docker::{startup_ping, BollardDocker};
use health::{router as health_router, AppState};
use heartbeat::Heartbeat;
use heartbeat_reporter::{CapacityReport, HeartbeatReporter};
use lifecycle::DeploymentLocks;
use node::{advertise_address, Node};
use prober::{DiscoveryDefaults, ProbeConfig, Prober, StatusCache};
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

    let node_id_hint = cfg.node_id.clone().unwrap_or_else(|| "pending".into());
    let otel_cfg = observability::OtelConfig::from_env(&cfg.service_name, &cfg.env, &node_id_hint);
    let otel = std::sync::Arc::new(observability::OtelHandle::init(&otel_cfg));
    let otel_shutdown = std::sync::Arc::clone(&otel);

    info!(
        service = %cfg.service_name,
        port = cfg.port,
        otel_enabled = otel_cfg.enabled,
        otel_endpoint = %otel_cfg.endpoint,
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
        log_default_tail = cfg.log_default_tail,
        log_stream_buffer = cfg.log_stream_buffer,
        stop_grace_seconds = cfg.stop_grace.as_secs(),
        on_config_conflict = ?cfg.on_config_conflict,
        control_url = cfg.control_url.as_deref().unwrap_or(""),
        reconcile_interval_seconds = cfg.reconcile_interval.as_secs(),
        control_report_mode = ?cfg.control_report_mode,
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

    let node = Node::bootstrap_with_id_and_keys(
        &cfg.data_dir,
        &docker,
        cfg.node_id.as_deref(),
        cfg.key_dir.as_deref(),
    )
    .await?;
    let workload_labels = node.labels();
    info!(
        node_id = %node.info.id,
        node_slots = cfg.node_slots,
        has_wireguard_public_key = node.wireguard_public_key.is_some(),
        label = %node::NODE_ID_LABEL,
        label_value = %workload_labels
            .get(node::NODE_ID_LABEL)
            .map(String::as_str)
            .unwrap_or(""),
        "node identity ready for workload labeling"
    );

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
    let mut prober_builder = Prober::new(
        Arc::clone(&docker) as Arc<dyn docker::DockerEngine>,
        Arc::new(StatusCache::new()),
        probe_cfg,
    )?;
    if let Some(discovery_url) = cfg.discovery_url.as_deref() {
        match DiscoveryClient::new(
            discovery_url,
            node.info.id.clone(),
            cfg.discovery_register_enabled,
            cfg.discovery_lease_seconds,
        ) {
            Ok(client) => {
                info!(
                    discovery_url = %discovery_url,
                    enabled = cfg.discovery_register_enabled,
                    lease_seconds = cfg.discovery_lease_seconds,
                    "wiring discovery registration client"
                );
                prober_builder = prober_builder.with_discovery(
                    Arc::new(client),
                    DiscoveryDefaults {
                        project: cfg.discovery_default_project.clone(),
                        environment: cfg.discovery_default_environment.clone(),
                    },
                );
            }
            Err(err) => {
                error!(error = %err, "invalid discovery client config; registration disabled");
            }
        }
    } else {
        info!("discovery registration disabled (FORGE_DISCOVERY_URL unset)");
    }
    let prober = Arc::new(prober_builder);
    let _prober_task = prober.spawn();

    let node = Arc::new(node);
    let deployment_locks = Arc::new(DeploymentLocks::new());

    let _control_heartbeat_task = if let Some(control_url) = cfg.control_url.as_deref() {
        let address = advertise_address(cfg.node_address.as_deref(), cfg.port);
        let cpu_millis = node.info.cpu.saturating_mul(1000);
        let mem_mb = if node.info.memory_bytes > 0 {
            Some((node.info.memory_bytes / (1024 * 1024)) as u32)
        } else {
            None
        };
        match HeartbeatReporter::new_with_join(
            control_url,
            node.info.id.clone(),
            address,
            CapacityReport {
                slots: cfg.node_slots,
                cpu_millis: Some(cpu_millis),
                mem_mb,
            },
            cfg.bootstrap_token.clone(),
            node.wireguard_public_key
                .as_ref()
                .map(|k| k.as_str().to_string()),
            Arc::clone(&docker) as Arc<dyn docker::DockerEngine>,
        ) {
            Ok(reporter) => {
                info!(
                    control_url = %control_url,
                    node_id = %node.info.id,
                    slots = cfg.node_slots,
                    interval_ms = cfg.control_heartbeat_interval.as_millis() as u64,
                    "starting control node register/heartbeat reporter"
                );
                Some(Arc::new(reporter).spawn(cfg.control_heartbeat_interval))
            }
            Err(err) => {
                error!(error = %err, "invalid control heartbeat reporter config");
                None
            }
        }
    } else {
        info!("control node register/heartbeat disabled (FORGE_CONTROL_URL unset)");
        None
    };

    let _reconcile_task = if let Some(control_url) = cfg.control_url.as_deref() {
        match ControlClient::new(control_url, node.info.id.clone()) {
            Ok(client) => {
                info!(
                    control_url = %control_url,
                    node_id = %node.info.id,
                    interval_seconds = cfg.reconcile_interval.as_secs(),
                    report_mode = ?cfg.control_report_mode,
                    "starting control reconcile loop"
                );
                Some(spawn_reconciler(ReconcilerConfig {
                    docker: Arc::clone(&docker) as Arc<dyn docker::DockerEngine>,
                    node: Arc::clone(&node),
                    locks: Arc::clone(&deployment_locks),
                    prober: Arc::clone(&prober),
                    control: client,
                    interval: cfg.reconcile_interval,
                    pull_timeout: cfg.pull_timeout,
                    stop_grace: cfg.stop_grace,
                    on_conflict: cfg.on_config_conflict,
                    report_mode: cfg.control_report_mode,
                    lifecycle_owner: cfg.lifecycle_owner,
                }))
            }
            Err(err) => {
                error!(error = %err, "invalid FORGE_CONTROL_URL; reconcile loop disabled");
                None
            }
        }
    } else {
        info!("control reconcile loop disabled (FORGE_CONTROL_URL unset)");
        None
    };

    let _peer_poll_task = if let Some(network_url) = cfg.network_url.as_deref() {
        match node.wireguard_public_key.as_ref() {
            Some(pk) => {
                let kind = network::WgBackendKind::parse(&cfg.network_wg_backend)
                    .unwrap_or(network::WgBackendKind::Userspace);
                let backend = network::select_backend(kind);
                let route_backend =
                    network::select_route_backend(&cfg.network_route_backend);
                info!(
                    network_url = %network_url,
                    network_name = %cfg.network_name,
                    backend = ?backend.kind(),
                    docker_colocated = cfg.node_docker_colocated,
                    membership = ?cfg.node_network_membership,
                    poll_interval_s = cfg.network_peer_poll_interval.as_secs(),
                    "starting network peer/route poll loop"
                );
                Some(network::spawn_peer_poll_loop(network::PeerPollConfig {
                    network_url: network_url.to_string(),
                    network_name: cfg.network_name.clone(),
                    node_id: node.info.id.clone(),
                    public_key: pk.as_str().to_string(),
                    endpoint: cfg.network_wg_endpoint.clone(),
                    iface: cfg.network_wg_iface.clone(),
                    poll_interval: cfg.network_peer_poll_interval,
                    backend,
                    route_backend,
                    docker_colocated: cfg.node_docker_colocated,
                    network_membership: cfg.node_network_membership.clone(),
                    private_iface: cfg.network_private_iface.clone(),
                    local_cidr: None,
                }))
            }
            None => {
                info!("wireguard peer poll skipped (no node public key)");
                None
            }
        }
    } else {
        info!("wireguard peer poll disabled (FORGE_NETWORK_URL unset)");
        None
    };

    let state = AppState {
        docker,
        node,
        heartbeat,
        pull_timeout: cfg.pull_timeout,
        prober: Arc::clone(&prober),
        log_default_tail: cfg.log_default_tail,
        log_stream_buffer: cfg.log_stream_buffer,
        stop_grace: cfg.stop_grace,
        on_config_conflict: cfg.on_config_conflict,
        deployment_locks,
    };

    let otel_layer = std::sync::Arc::clone(&otel);
    let app = axum::Router::new()
        .merge(health_router())
        .merge(routes::node::router())
        .merge(routes::node_state::router())
        .merge(routes::workloads::router())
        .merge(routes::status::router())
        .merge(routes::logs::router())
        .layer(axum::middleware::from_fn(move |req, next| {
            let handle = std::sync::Arc::clone(&otel_layer);
            async move { observability::middleware(handle, req, next).await }
        }))
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

    if let Some(discovery) = prober.discovery() {
        discovery.deregister_all().await;
    }

    otel_shutdown.shutdown();
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
