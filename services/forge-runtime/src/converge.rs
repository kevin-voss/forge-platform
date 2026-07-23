use crate::control_client::{
    ControlClient, ControlError, DeploymentStatusReport, DesiredDeployment, EndpointReport,
};
use crate::docker::DockerEngine;
use crate::lifecycle::{self, DeploymentLocks, OnConfigConflict};
use crate::node::Node;
use crate::prober::{note_workload_created, Prober};
use crate::status::{to_control_status, WorkloadStatus};
use crate::workload::{WorkloadSpec, DEPLOYMENT_ID_LABEL, MANAGED_LABEL, MANAGED_LABEL_VALUE};
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::task::JoinHandle;
use tracing::{info, warn};

/// Who owns create/stop convergence for desired deployments.
///
/// Epic 07 moves lifecycle ownership to Control's reconcile controller.
/// When `Control`, Runtime still polls/reports but does not create/delete.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum LifecycleOwner {
    #[default]
    Runtime,
    Control,
}

impl LifecycleOwner {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "runtime" => Ok(Self::Runtime),
            "control" => Ok(Self::Control),
            other => Err(format!(
                "FORGE_LIFECYCLE_OWNER must be runtime|control, got {other:?}"
            )),
        }
    }
}

/// How Runtime reports actual state to Control.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ReportMode {
    #[default]
    Push,
    Pull,
}

impl ReportMode {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "push" => Ok(Self::Push),
            "pull" => Ok(Self::Pull),
            other => Err(format!(
                "FORGE_CONTROL_REPORT_MODE must be push|pull, got {other:?}"
            )),
        }
    }
}

/// Pure set math for converge planning (unit-tested).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConvergePlan {
    pub to_create: Vec<String>,
    pub to_delete: Vec<String>,
    pub to_ensure: Vec<String>,
}

/// Compute create/delete/ensure sets from desired and actual deployment ids.
pub fn plan_converge(desired: &HashSet<String>, actual: &HashSet<String>) -> ConvergePlan {
    let mut to_create: Vec<String> = desired.difference(actual).cloned().collect();
    let mut to_delete: Vec<String> = actual.difference(desired).cloned().collect();
    let mut to_ensure: Vec<String> = desired.intersection(actual).cloned().collect();
    to_create.sort();
    to_delete.sort();
    to_ensure.sort();
    ConvergePlan {
        to_create,
        to_delete,
        to_ensure,
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ConvergeCounts {
    pub created: u32,
    pub deleted: u32,
    pub ensured: u32,
    pub failed: u32,
    pub reported: u32,
}

/// Shared dependencies for a converge / reconcile cycle.
pub struct ConvergeCtx<'a> {
    pub docker: &'a dyn DockerEngine,
    pub node: &'a Node,
    pub locks: &'a DeploymentLocks,
    pub prober: &'a Prober,
    pub control: &'a ControlClient,
    pub pull_timeout: Duration,
    pub stop_grace: Duration,
    pub on_conflict: OnConfigConflict,
    pub report_mode: ReportMode,
    pub failure_backoff: &'a FailureBackoff,
    pub lifecycle_owner: LifecycleOwner,
}

/// Single-shot desired↔actual convergence using idempotent create + delete.
pub async fn converge_once(ctx: &ConvergeCtx<'_>, desired: &[DesiredDeployment]) -> ConvergeCounts {
    let desired_active: Vec<&DesiredDeployment> =
        desired.iter().filter(|d| d.is_desired()).collect();
    let desired_ids: HashSet<String> = desired_active.iter().map(|d| d.id.clone()).collect();

    let actual_ids = match list_actual_deployment_ids(ctx.docker).await {
        Ok(ids) => ids,
        Err(err) => {
            warn!(error = %err, "converge: list actual failed; skipping cycle");
            return ConvergeCounts::default();
        }
    };

    let plan = plan_converge(&desired_ids, &actual_ids);
    let mut counts = ConvergeCounts::default();

    // Epic 07: Control owns create/stop; Runtime only observes/reports.
    if ctx.lifecycle_owner == LifecycleOwner::Control {
        if ctx.report_mode == ReportMode::Push {
            counts.reported += report_actuals(ctx, &desired_ids).await;
        }
        info!(
            created = 0u32,
            deleted = 0u32,
            ensured = 0u32,
            failed = counts.failed,
            reported = counts.reported,
            desired = desired_ids.len(),
            actual = actual_ids.len(),
            lifecycle_owner = "control",
            "converge cycle complete (lifecycle delegated to Control)"
        );
        return counts;
    }

    let by_id: HashMap<&str, &DesiredDeployment> =
        desired_active.iter().map(|d| (d.id.as_str(), *d)).collect();

    for id in plan.to_create.iter().chain(plan.to_ensure.iter()) {
        let Some(dep) = by_id.get(id.as_str()).copied() else {
            continue;
        };
        if ctx.failure_backoff.should_skip(&dep.id) {
            tracing::debug!(
                deployment_id = %dep.id,
                "converge: skipping ensure due to failure backoff"
            );
            continue;
        }
        let was_new = !actual_ids.contains(&dep.id);
        match ensure_desired(ctx, dep).await {
            Ok(()) => {
                ctx.failure_backoff.clear(&dep.id);
                if was_new {
                    counts.created += 1;
                } else {
                    counts.ensured += 1;
                }
            }
            Err(err) => {
                warn!(
                    deployment_id = %dep.id,
                    error = %err,
                    "converge: ensure failed"
                );
                ctx.failure_backoff.record_failure(&dep.id);
                counts.failed += 1;
                if ctx.report_mode == ReportMode::Push {
                    let _ = report_one(ctx.control, &dep.id, "failed", None).await;
                    counts.reported += 1;
                }
            }
        }
    }

    match lifecycle::cleanup_orphans(ctx.docker, &desired_ids, ctx.stop_grace).await {
        Ok(removed) => {
            for id in &removed {
                ctx.prober.cache().mark_stopped_by_operator(id);
                ctx.prober.cache().remove(id);
            }
            counts.deleted = removed.len() as u32;
        }
        Err(err) => {
            warn!(error = %err.message(), "converge: orphan cleanup failed");
            counts.failed += 1;
        }
    }

    if ctx.report_mode == ReportMode::Push {
        counts.reported += report_actuals(ctx, &desired_ids).await;
    }

    info!(
        created = counts.created,
        deleted = counts.deleted,
        ensured = counts.ensured,
        failed = counts.failed,
        reported = counts.reported,
        desired = desired_ids.len(),
        actual = actual_ids.len(),
        "converge cycle complete"
    );
    counts
}

async fn ensure_desired(ctx: &ConvergeCtx<'_>, dep: &DesiredDeployment) -> Result<(), String> {
    let port = if dep.port == 0 { 8080 } else { dep.port };
    let spec = WorkloadSpec {
        deployment_id: dep.id.clone(),
        image: dep.image.clone(),
        port,
        environment: dep.workload_environment(),
        secrets_fingerprint: None,
        limits: None,
        };
    let outcome = lifecycle::ensure_workload(
        ctx.docker,
        ctx.node,
        ctx.locks,
        spec,
        ctx.pull_timeout,
        ctx.stop_grace,
        ctx.on_conflict,
    )
    .await
    .map_err(|e| e.message().to_string())?;
    let view = outcome.view();
    note_workload_created(
        ctx.prober.cache().as_ref(),
        &view.deployment_id,
        view.host_port,
        port,
        &view.container_id,
    );
    Ok(())
}

async fn list_actual_deployment_ids(docker: &dyn DockerEngine) -> Result<HashSet<String>, String> {
    let listed = docker.list_managed_containers().await?;
    let mut ids = HashSet::new();
    for inspect in listed {
        let labels = match &inspect.labels {
            Some(l) => l,
            None => continue,
        };
        if labels.get(MANAGED_LABEL).map(String::as_str) != Some(MANAGED_LABEL_VALUE) {
            continue;
        }
        if let Some(id) = labels.get(DEPLOYMENT_ID_LABEL) {
            if !id.is_empty() {
                ids.insert(id.clone());
            }
        }
    }
    Ok(ids)
}

async fn report_actuals(ctx: &ConvergeCtx<'_>, desired_ids: &HashSet<String>) -> u32 {
    let mut reported = 0u32;
    for id in desired_ids {
        let status = match ctx.prober.status_for(id).await {
            Ok(view) => view.status,
            Err(_) => match crate::workload::get_workload(ctx.docker, id).await {
                Ok(view) if view.state == "failed" => WorkloadStatus::Failed,
                Ok(_) => WorkloadStatus::Starting,
                Err(_) => continue,
            },
        };
        let control_status = to_control_status(status);
        let host_port = crate::workload::get_workload(ctx.docker, id)
            .await
            .ok()
            .map(|v| v.host_port)
            .filter(|p| *p > 0);
        if report_one(ctx.control, id, control_status, host_port)
            .await
            .is_ok()
        {
            reported += 1;
        }
    }
    reported
}

async fn report_one(
    control: &ControlClient,
    deployment_id: &str,
    status: &str,
    host_port: Option<u16>,
) -> Result<(), ControlError> {
    let report = DeploymentStatusReport {
        status: status.to_string(),
        node_id: control.node_id().to_string(),
        endpoint: host_port.map(|host_port| EndpointReport { host_port }),
    };
    control.report_status(deployment_id, &report).await
}

/// Tracks failed ensure attempts so pull failures are not retried every cycle.
#[derive(Debug, Default)]
pub struct FailureBackoff {
    inner: Mutex<HashMap<String, Instant>>,
    base: Duration,
    max: Duration,
}

impl FailureBackoff {
    pub fn new() -> Self {
        Self {
            inner: Mutex::new(HashMap::new()),
            base: Duration::from_secs(15),
            max: Duration::from_secs(120),
        }
    }

    pub fn should_skip(&self, deployment_id: &str) -> bool {
        let guard = self.inner.lock().expect("backoff");
        match guard.get(deployment_id) {
            Some(at) => at.elapsed() < self.base.min(self.max),
            None => false,
        }
    }

    pub fn record_failure(&self, deployment_id: &str) {
        self.inner
            .lock()
            .expect("backoff")
            .insert(deployment_id.to_string(), Instant::now());
    }

    pub fn clear(&self, deployment_id: &str) {
        self.inner.lock().expect("backoff").remove(deployment_id);
    }
}

/// Runtime wiring for the periodic reconcile task.
pub struct ReconcilerConfig {
    pub docker: Arc<dyn DockerEngine>,
    pub node: Arc<Node>,
    pub locks: Arc<DeploymentLocks>,
    pub prober: Arc<Prober>,
    pub control: ControlClient,
    pub interval: Duration,
    pub pull_timeout: Duration,
    pub stop_grace: Duration,
    pub on_conflict: OnConfigConflict,
    pub report_mode: ReportMode,
    pub lifecycle_owner: LifecycleOwner,
}

/// Periodic reconcile task: fetch desired → converge → report.
pub fn spawn_reconciler(cfg: ReconcilerConfig) -> JoinHandle<()> {
    let backoff = Arc::new(FailureBackoff::new());
    tokio::spawn(async move {
        let mut ticker = tokio::time::interval(cfg.interval);
        ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
        loop {
            ticker.tick().await;
            match cfg.control.fetch_desired().await {
                Ok(desired) => {
                    let ctx = ConvergeCtx {
                        docker: cfg.docker.as_ref(),
                        node: cfg.node.as_ref(),
                        locks: cfg.locks.as_ref(),
                        prober: cfg.prober.as_ref(),
                        control: &cfg.control,
                        pull_timeout: cfg.pull_timeout,
                        stop_grace: cfg.stop_grace,
                        on_conflict: cfg.on_conflict,
                        report_mode: cfg.report_mode,
                        failure_backoff: backoff.as_ref(),
                        lifecycle_owner: cfg.lifecycle_owner,
                    };
                    converge_once(&ctx, &desired).await;
                }
                Err(err) => {
                    warn!(
                        error = %err,
                        "control unreachable or error; skipping converge cycle (no local churn)"
                    );
                }
            }
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::{RecordingDocker, StubDocker};
    use crate::prober::{ProbeConfig, StatusCache};
    use httpmock::prelude::*;
    use tempfile::tempdir;

    #[test]
    fn set_math_create_delete_ensure() {
        let desired = HashSet::from(["a".into(), "b".into(), "c".into()]);
        let actual = HashSet::from(["b".into(), "c".into(), "d".into()]);
        let plan = plan_converge(&desired, &actual);
        assert_eq!(plan.to_create, vec!["a".to_string()]);
        assert_eq!(plan.to_delete, vec!["d".to_string()]);
        assert_eq!(plan.to_ensure, vec!["b".to_string(), "c".to_string()]);
    }

    #[test]
    fn set_math_empty_desired_deletes_all() {
        let desired = HashSet::new();
        let actual = HashSet::from(["x".into()]);
        let plan = plan_converge(&desired, &actual);
        assert!(plan.to_create.is_empty());
        assert_eq!(plan.to_delete, vec!["x".to_string()]);
    }

    #[test]
    fn report_mode_parse() {
        assert_eq!(ReportMode::parse("push").unwrap(), ReportMode::Push);
        assert_eq!(ReportMode::parse("PULL").unwrap(), ReportMode::Pull);
        assert!(ReportMode::parse("both").is_err());
    }

    fn test_ctx<'a>(
        docker: &'a dyn DockerEngine,
        node: &'a Node,
        locks: &'a DeploymentLocks,
        prober: &'a Prober,
        control: &'a ControlClient,
        backoff: &'a FailureBackoff,
    ) -> ConvergeCtx<'a> {
        ConvergeCtx {
            docker,
            node,
            locks,
            prober,
            control,
            pull_timeout: Duration::from_secs(5),
            stop_grace: Duration::from_secs(1),
            on_conflict: OnConfigConflict::Recreate,
            report_mode: ReportMode::Pull,
            failure_backoff: backoff,
            lifecycle_owner: LifecycleOwner::Runtime,
        }
    }

    #[tokio::test]
    async fn converge_creates_missing_desired() {
        let docker = Arc::new(RecordingDocker::ok(49152));
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker) as Arc<dyn DockerEngine>,
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .unwrap(),
        );

        let server = MockServer::start();
        let control = ControlClient::new(server.base_url(), node.info.id.clone()).unwrap();
        let backoff = FailureBackoff::new();
        let desired = vec![DesiredDeployment {
            id: "keep-me".into(),
            image: "localhost:5000/demo-go:latest".into(),
            port: 8080,
            desired_replicas: 1,
            service_id: None,
            environment_id: None,
            environment: HashMap::new(),
        }];

        let ctx = test_ctx(
            docker.as_ref(),
            &node,
            &locks,
            prober.as_ref(),
            &control,
            &backoff,
        );
        let counts = converge_once(&ctx, &desired).await;

        assert_eq!(counts.created, 1);
        assert_eq!(counts.failed, 0);
        assert_eq!(
            docker.created_name.lock().unwrap().as_deref(),
            Some("forge-keep-me")
        );
    }

    #[tokio::test]
    async fn converge_deletes_undesired_actual() {
        let docker = Arc::new(RecordingDocker::ok(49152));
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker) as Arc<dyn DockerEngine>,
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .unwrap(),
        );

        lifecycle::ensure_workload(
            docker.as_ref(),
            &node,
            &locks,
            WorkloadSpec {
                deployment_id: "orphan".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
                limits: None,
        },
            Duration::from_secs(5),
            Duration::from_secs(1),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();

        let server = MockServer::start();
        let control = ControlClient::new(server.base_url(), node.info.id.clone()).unwrap();
        let backoff = FailureBackoff::new();
        let ctx = test_ctx(
            docker.as_ref(),
            &node,
            &locks,
            prober.as_ref(),
            &control,
            &backoff,
        );
        let counts = converge_once(&ctx, &[]).await;

        assert_eq!(counts.deleted, 1);
        assert!(docker.created_name.lock().unwrap().is_none());
        assert!(
            docker
                .remove_calls
                .load(std::sync::atomic::Ordering::SeqCst)
                >= 1
        );
    }

    #[tokio::test]
    async fn control_down_skips_without_churn() {
        let docker = RecordingDocker::ok(49152);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        let _ = lifecycle::ensure_workload(
            &docker,
            &node,
            &locks,
            WorkloadSpec {
                deployment_id: "existing".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
                limits: None,
        },
            Duration::from_secs(5),
            Duration::from_secs(1),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();

        let before = docker.calls.lock().unwrap().clone();
        let client = ControlClient::new("http://127.0.0.1:1", &node.info.id).unwrap();
        let err = client.fetch_desired().await.expect_err("down");
        assert!(matches!(err, ControlError::Unreachable(_)));
        let after = docker.calls.lock().unwrap().clone();
        assert_eq!(before, after, "no docker churn when control is down");
    }
}
