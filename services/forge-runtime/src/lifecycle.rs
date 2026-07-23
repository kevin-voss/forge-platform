use crate::docker::{ContainerInspectInfo, DockerEngine};
use crate::node::Node;
use crate::workload::{
    self, container_name, validate_spec, WorkloadError, WorkloadSpec, WorkloadView,
    DEPLOYMENT_ID_LABEL, MANAGED_LABEL, MANAGED_LABEL_VALUE,
};
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tracing::{info, warn};

/// Behavior when an existing container's image/config conflicts with the request.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum OnConfigConflict {
    /// Stop+remove the old container and create a new one.
    #[default]
    Recreate,
    /// Return HTTP 409 without mutating the existing container.
    Reject,
}

impl OnConfigConflict {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "recreate" => Ok(Self::Recreate),
            "reject" => Ok(Self::Reject),
            other => Err(format!(
                "FORGE_ON_CONFIG_CONFLICT must be recreate|reject, got {other:?}"
            )),
        }
    }
}

/// Result of an idempotent create/ensure.
#[derive(Debug, Clone, PartialEq)]
pub enum EnsureOutcome {
    /// Brand-new container created (`201`).
    Created(WorkloadView),
    /// Existing container reused or restarted (`200`).
    Existing(WorkloadView),
}

impl EnsureOutcome {
    pub fn view(&self) -> &WorkloadView {
        match self {
            Self::Created(v) | Self::Existing(v) => v,
        }
    }
}

/// Pure decision for idempotent create (unit-tested table).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IdempotentAction {
    CreateNew,
    ReturnExisting,
    StartExisting,
    Recreate,
    RejectConflict,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ExistingFacts {
    pub running: bool,
    pub image: String,
}

/// Decide what to do given an optional existing managed container.
pub fn decide_idempotent_action(
    existing: Option<&ExistingFacts>,
    requested_image: &str,
    on_conflict: OnConfigConflict,
) -> IdempotentAction {
    let Some(existing) = existing else {
        return IdempotentAction::CreateNew;
    };

    let same_image = images_match(&existing.image, requested_image);
    if existing.running && same_image {
        return IdempotentAction::ReturnExisting;
    }
    if !existing.running && same_image {
        return IdempotentAction::StartExisting;
    }
    // Image/config conflict.
    match on_conflict {
        OnConfigConflict::Recreate => IdempotentAction::Recreate,
        OnConfigConflict::Reject => IdempotentAction::RejectConflict,
    }
}

fn images_match(existing: &str, requested: &str) -> bool {
    normalize_image(existing) == normalize_image(requested)
}

fn normalize_image(image: &str) -> String {
    image.split('@').next().unwrap_or(image).trim().to_string()
}

fn is_running_state(state: &str) -> bool {
    matches!(
        state.to_ascii_lowercase().as_str(),
        "running" | "restarting"
    )
}

fn is_managed(inspect: &ContainerInspectInfo) -> bool {
    inspect
        .labels
        .as_ref()
        .and_then(|l| l.get(MANAGED_LABEL))
        .map(String::as_str)
        == Some(MANAGED_LABEL_VALUE)
}

fn deployment_label_matches(inspect: &ContainerInspectInfo, deployment_id: &str) -> bool {
    match inspect
        .labels
        .as_ref()
        .and_then(|l| l.get(DEPLOYMENT_ID_LABEL))
    {
        Some(id) => id == deployment_id,
        None => true, // name match is enough when label absent but managed
    }
}

/// Look up an existing managed container for a deployment (name, then label).
pub async fn find_existing_managed(
    docker: &dyn DockerEngine,
    deployment_id: &str,
) -> Result<Option<ContainerInspectInfo>, WorkloadError> {
    let name = container_name(deployment_id);
    match docker.inspect_container(&name).await {
        Ok(inspect) => {
            if is_managed(&inspect) && deployment_label_matches(&inspect, deployment_id) {
                return Ok(Some(inspect));
            }
            // Name collision with an unmanaged container — ignore for managed lookup.
            Ok(None)
        }
        Err(err) => {
            if err.contains("not found") || err.contains("No such container") {
                // Fall back to label scan (handles renamed/orphaned name mismatches).
                let listed = docker
                    .list_managed_containers()
                    .await
                    .map_err(WorkloadError::Inspect)?;
                let match_ = listed.into_iter().find(|info| {
                    info.labels
                        .as_ref()
                        .and_then(|l| l.get(DEPLOYMENT_ID_LABEL))
                        .map(String::as_str)
                        == Some(deployment_id)
                });
                Ok(match_)
            } else {
                Err(WorkloadError::Inspect(err))
            }
        }
    }
}

fn facts_from_inspect(inspect: &ContainerInspectInfo) -> ExistingFacts {
    ExistingFacts {
        running: is_running_state(&inspect.state),
        image: inspect.image.clone().unwrap_or_default(),
    }
}

fn view_from_inspect(deployment_id: &str, inspect: &ContainerInspectInfo) -> WorkloadView {
    let container_port = inspect
        .port_bindings
        .keys()
        .find_map(|k| k.strip_suffix("/tcp").and_then(|p| p.parse::<u16>().ok()))
        .unwrap_or(0);
    let host_port = if container_port == 0 {
        inspect
            .port_bindings
            .values()
            .flatten()
            .copied()
            .next()
            .unwrap_or(0)
    } else {
        inspect
            .port_bindings
            .get(&format!("{container_port}/tcp"))
            .and_then(|p| p.first().copied())
            .unwrap_or(0)
    };
    let state = if is_running_state(&inspect.state) {
        "starting".into()
    } else if matches!(
        inspect.state.to_ascii_lowercase().as_str(),
        "exited" | "dead"
    ) {
        "failed".into()
    } else {
        "stopped".into()
    };
    let secrets_fingerprint = inspect
        .labels
        .as_ref()
        .and_then(|l| {
            l.get(crate::workload::env::SECRETS_FINGERPRINT_LABEL)
                .cloned()
        })
        .filter(|s| !s.is_empty());
    WorkloadView {
        deployment_id: deployment_id.to_string(),
        container_id: inspect.id.clone(),
        host_port,
        state,
        image: inspect.image.clone(),
        secrets_fingerprint,
    }
}

/// Per-deployment-id lock table so concurrent creates serialize.
#[derive(Debug, Default)]
pub struct DeploymentLocks {
    inner: Mutex<HashMap<String, Arc<tokio::sync::Mutex<()>>>>,
}

impl DeploymentLocks {
    pub fn new() -> Self {
        Self::default()
    }

    pub async fn lock(&self, deployment_id: &str) -> tokio::sync::OwnedMutexGuard<()> {
        let mutex = {
            let mut map = self.inner.lock().expect("deployment locks");
            map.entry(deployment_id.to_string())
                .or_insert_with(|| Arc::new(tokio::sync::Mutex::new(())))
                .clone()
        };
        mutex.lock_owned().await
    }
}

/// Idempotent ensure: reuse/start/recreate or create new. Holds a per-id lock.
pub async fn ensure_workload(
    docker: &dyn DockerEngine,
    node: &Node,
    locks: &DeploymentLocks,
    spec: WorkloadSpec,
    pull_timeout: Duration,
    stop_grace: Duration,
    on_conflict: OnConfigConflict,
) -> Result<EnsureOutcome, WorkloadError> {
    let spec = validate_spec(&spec)?;
    let _guard = locks.lock(&spec.deployment_id).await;

    let existing = find_existing_managed(docker, &spec.deployment_id).await?;
    let facts = existing.as_ref().map(facts_from_inspect);
    let action = decide_idempotent_action(facts.as_ref(), &spec.image, on_conflict);

    match action {
        IdempotentAction::CreateNew => {
            let view = workload::create_and_start(docker, node, spec, pull_timeout).await?;
            Ok(EnsureOutcome::Created(view))
        }
        IdempotentAction::ReturnExisting => {
            let inspect = existing.expect("existing");
            info!(
                deployment_id = %spec.deployment_id,
                container_id = %inspect.id,
                "existing container reused"
            );
            Ok(EnsureOutcome::Existing(view_from_inspect(
                &spec.deployment_id,
                &inspect,
            )))
        }
        IdempotentAction::StartExisting => {
            let inspect = existing.expect("existing");
            info!(
                deployment_id = %spec.deployment_id,
                container_id = %inspect.id,
                "starting stopped container"
            );
            docker
                .start_container(&inspect.id)
                .await
                .map_err(WorkloadError::Start)?;
            let refreshed = docker
                .inspect_container(&inspect.id)
                .await
                .map_err(WorkloadError::Inspect)?;
            Ok(EnsureOutcome::Existing(view_from_inspect(
                &spec.deployment_id,
                &refreshed,
            )))
        }
        IdempotentAction::Recreate => {
            let inspect = existing.expect("existing");
            info!(
                deployment_id = %spec.deployment_id,
                container_id = %inspect.id,
                old_image = ?inspect.image,
                new_image = %spec.image,
                "config conflict; recreating container"
            );
            stop_and_remove_managed(docker, &inspect, stop_grace).await?;
            let view = workload::create_and_start(docker, node, spec, pull_timeout).await?;
            Ok(EnsureOutcome::Created(view))
        }
        IdempotentAction::RejectConflict => {
            let inspect = existing.expect("existing");
            Err(WorkloadError::Conflict(format!(
                "workload for deployment_id {} exists with conflicting image (have {:?}, want {})",
                spec.deployment_id, inspect.image, spec.image
            )))
        }
    }
}

/// Gracefully stop then remove a managed container. Idempotent when absent.
pub async fn delete_workload(
    docker: &dyn DockerEngine,
    locks: &DeploymentLocks,
    deployment_id: &str,
    stop_grace: Duration,
) -> Result<(), WorkloadError> {
    let deployment_id = deployment_id.trim();
    if deployment_id.is_empty() || !workload::is_valid_deployment_id_for_delete(deployment_id) {
        return Err(WorkloadError::Validation("deployment_id is invalid".into()));
    }

    let _guard = locks.lock(deployment_id).await;

    let Some(inspect) = find_existing_managed(docker, deployment_id).await? else {
        info!(
            deployment_id = %deployment_id,
            "delete requested but no managed container; idempotent success"
        );
        return Ok(());
    };

    stop_and_remove_managed(docker, &inspect, stop_grace).await?;
    info!(
        deployment_id = %deployment_id,
        container_id = %inspect.id,
        "workload deleted"
    );
    Ok(())
}

async fn stop_and_remove_managed(
    docker: &dyn DockerEngine,
    inspect: &ContainerInspectInfo,
    stop_grace: Duration,
) -> Result<(), WorkloadError> {
    if !is_managed(inspect) {
        return Err(WorkloadError::Validation(
            "refusing to stop/remove unmanaged container".into(),
        ));
    }

    let id = inspect.id.as_str();
    let grace_secs = stop_grace.as_secs();

    if is_running_state(&inspect.state) {
        info!(
            container_id = %id,
            grace_seconds = grace_secs,
            "stopping container (SIGTERM + grace)"
        );
        match docker.stop_container(id, grace_secs).await {
            Ok(()) => {
                info!(container_id = %id, grace_seconds = grace_secs, "container stopped");
            }
            Err(err) => {
                // Already stopped is fine; otherwise escalate to force remove.
                if err.contains("already stopped") || err.contains("is not running") {
                    info!(container_id = %id, "container already stopped");
                } else {
                    warn!(
                        container_id = %id,
                        error = %err,
                        "stop failed; escalating to force remove"
                    );
                }
            }
        }

        // If still running after stop (stub / pathological), force-kill via remove.
        if let Ok(after) = docker.inspect_container(id).await {
            if is_running_state(&after.state) {
                warn!(
                    container_id = %id,
                    grace_seconds = grace_secs,
                    "container still running after grace; force kill via remove"
                );
            }
        }
    } else {
        info!(
            container_id = %id,
            state = %inspect.state,
            "container not running; skipping stop"
        );
    }

    remove_with_retry(docker, id).await
}

async fn remove_with_retry(
    docker: &dyn DockerEngine,
    id_or_name: &str,
) -> Result<(), WorkloadError> {
    const ATTEMPTS: u32 = 3;
    let mut last_err = String::new();
    for attempt in 1..=ATTEMPTS {
        match docker.remove_container(id_or_name, true).await {
            Ok(()) => {
                info!(container = %id_or_name, attempt, "container removed");
                return Ok(());
            }
            Err(err) => {
                if err.contains("No such container") || err.contains("not found") {
                    return Ok(());
                }
                last_err = err;
                warn!(
                    container = %id_or_name,
                    attempt,
                    error = %last_err,
                    "container remove failed; retrying"
                );
                if attempt < ATTEMPTS {
                    tokio::time::sleep(Duration::from_millis(100 * u64::from(attempt))).await;
                }
            }
        }
    }
    Err(WorkloadError::Remove(last_err))
}

/// Remove Forge-managed containers whose `forge.deployment_id` is not in `known`.
/// Returns the deployment ids that were removed.
pub async fn cleanup_orphans(
    docker: &dyn DockerEngine,
    known_deployment_ids: &HashSet<String>,
    stop_grace: Duration,
) -> Result<Vec<String>, WorkloadError> {
    let listed = docker
        .list_managed_containers()
        .await
        .map_err(WorkloadError::Inspect)?;

    let mut removed = Vec::new();
    for inspect in listed {
        if !is_managed(&inspect) {
            continue;
        }
        let Some(dep_id) = inspect
            .labels
            .as_ref()
            .and_then(|l| l.get(DEPLOYMENT_ID_LABEL))
            .cloned()
        else {
            continue;
        };
        if known_deployment_ids.contains(&dep_id) {
            continue;
        }
        info!(
            deployment_id = %dep_id,
            container_id = %inspect.id,
            "orphan cleanup: removing unreferenced managed container"
        );
        stop_and_remove_managed(docker, &inspect, stop_grace).await?;
        removed.push(dep_id);
    }
    Ok(removed)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::{RecordingDocker, StubDocker};
    use crate::node::Node;
    use std::sync::atomic::Ordering;
    use std::sync::Arc;
    use tempfile::tempdir;

    #[test]
    fn decision_table_absent_running_stopped_conflict() {
        assert_eq!(
            decide_idempotent_action(None, "img:v1", OnConfigConflict::Recreate),
            IdempotentAction::CreateNew
        );

        let running_same = ExistingFacts {
            running: true,
            image: "img:v1".into(),
        };
        assert_eq!(
            decide_idempotent_action(Some(&running_same), "img:v1", OnConfigConflict::Recreate),
            IdempotentAction::ReturnExisting
        );

        let running_diff = ExistingFacts {
            running: true,
            image: "img:v1".into(),
        };
        assert_eq!(
            decide_idempotent_action(Some(&running_diff), "img:v2", OnConfigConflict::Recreate),
            IdempotentAction::Recreate
        );
        assert_eq!(
            decide_idempotent_action(Some(&running_diff), "img:v2", OnConfigConflict::Reject),
            IdempotentAction::RejectConflict
        );

        let stopped_same = ExistingFacts {
            running: false,
            image: "img:v1".into(),
        };
        assert_eq!(
            decide_idempotent_action(Some(&stopped_same), "img:v1", OnConfigConflict::Recreate),
            IdempotentAction::StartExisting
        );

        let stopped_diff = ExistingFacts {
            running: false,
            image: "img:v1".into(),
        };
        assert_eq!(
            decide_idempotent_action(Some(&stopped_diff), "img:v2", OnConfigConflict::Recreate),
            IdempotentAction::Recreate
        );
    }

    #[tokio::test]
    async fn delete_when_absent_is_idempotent_ok() {
        let docker = RecordingDocker::missing();
        let locks = DeploymentLocks::new();
        delete_workload(&docker, &locks, "missing-dep", Duration::from_secs(1))
            .await
            .expect("idempotent delete");
        assert_eq!(docker.remove_calls.load(Ordering::SeqCst), 0);
        assert_eq!(docker.stop_calls.load(Ordering::SeqCst), 0);
    }

    #[tokio::test]
    async fn delete_stops_then_removes_managed() {
        let docker = RecordingDocker::ok(49152);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();

        ensure_workload(
            &docker,
            &node,
            &locks,
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
            },
            Duration::from_secs(5),
            Duration::from_secs(2),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();

        delete_workload(&docker, &locks, "deployment-123", Duration::from_secs(2))
            .await
            .unwrap();

        assert!(docker.stop_calls.load(Ordering::SeqCst) >= 1);
        assert!(docker.remove_calls.load(Ordering::SeqCst) >= 1);
        assert!(find_existing_managed(&docker, "deployment-123")
            .await
            .unwrap()
            .is_none());

        // Second delete still succeeds.
        delete_workload(&docker, &locks, "deployment-123", Duration::from_secs(2))
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn ensure_reuses_running_same_image() {
        let docker = RecordingDocker::ok(45555);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        let spec = WorkloadSpec {
            deployment_id: "deployment-123".into(),
            image: "localhost:5000/demo-go:latest".into(),
            port: 8080,
            environment: HashMap::new(),
            secrets_fingerprint: None,
        };

        let first = ensure_workload(
            &docker,
            &node,
            &locks,
            spec.clone(),
            Duration::from_secs(5),
            Duration::from_secs(2),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();
        assert!(matches!(first, EnsureOutcome::Created(_)));

        let create_count_before = docker
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|c| **c == "create")
            .count();

        let second = ensure_workload(
            &docker,
            &node,
            &locks,
            spec,
            Duration::from_secs(5),
            Duration::from_secs(2),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();
        assert!(matches!(second, EnsureOutcome::Existing(_)));
        assert_eq!(second.view().container_id, first.view().container_id);

        let create_count_after = docker
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|c| **c == "create")
            .count();
        assert_eq!(create_count_before, create_count_after);
    }

    #[tokio::test]
    async fn lock_serializes_concurrent_creates() {
        let docker = Arc::new(RecordingDocker::ok(40001));
        docker.set_create_delay(Duration::from_millis(80));
        let dir = tempdir().unwrap();
        let node = Arc::new(
            Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
                .await
                .unwrap(),
        );
        let locks = Arc::new(DeploymentLocks::new());
        let spec = WorkloadSpec {
            deployment_id: "deployment-race".into(),
            image: "localhost:5000/demo-go:latest".into(),
            port: 8080,
            environment: HashMap::new(),
            secrets_fingerprint: None,
        };

        let d1 = Arc::clone(&docker);
        let n1 = Arc::clone(&node);
        let l1 = Arc::clone(&locks);
        let s1 = spec.clone();
        let t1 = tokio::spawn(async move {
            ensure_workload(
                d1.as_ref(),
                n1.as_ref(),
                l1.as_ref(),
                s1,
                Duration::from_secs(5),
                Duration::from_secs(2),
                OnConfigConflict::Recreate,
            )
            .await
        });

        let d2 = Arc::clone(&docker);
        let n2 = Arc::clone(&node);
        let l2 = Arc::clone(&locks);
        let s2 = spec;
        let t2 = tokio::spawn(async move {
            ensure_workload(
                d2.as_ref(),
                n2.as_ref(),
                l2.as_ref(),
                s2,
                Duration::from_secs(5),
                Duration::from_secs(2),
                OnConfigConflict::Recreate,
            )
            .await
        });

        let r1 = t1.await.unwrap().unwrap();
        let r2 = t2.await.unwrap().unwrap();
        assert_eq!(r1.view().container_id, r2.view().container_id);

        let create_count = docker
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|c| **c == "create")
            .count();
        assert_eq!(create_count, 1, "only one create call under lock");
    }

    #[tokio::test]
    async fn refuses_to_touch_unmanaged_name_collision() {
        let docker = RecordingDocker::unmanaged_named("forge-deployment-x");
        let locks = DeploymentLocks::new();
        // Delete should be idempotent success (no managed workload) and must not stop/remove.
        delete_workload(&docker, &locks, "deployment-x", Duration::from_secs(1))
            .await
            .unwrap();
        assert_eq!(docker.stop_calls.load(Ordering::SeqCst), 0);
        assert_eq!(docker.remove_calls.load(Ordering::SeqCst), 0);
    }

    #[tokio::test]
    async fn orphan_cleanup_removes_unknown_managed() {
        let docker = RecordingDocker::ok(41000);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        ensure_workload(
            &docker,
            &node,
            &locks,
            WorkloadSpec {
                deployment_id: "orphan-1".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
            },
            Duration::from_secs(5),
            Duration::from_secs(1),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();

        let known = HashSet::new(); // nothing known → orphan
        let removed = cleanup_orphans(&docker, &known, Duration::from_secs(1))
            .await
            .unwrap();
        assert_eq!(removed, vec!["orphan-1".to_string()]);
        assert!(find_existing_managed(&docker, "orphan-1")
            .await
            .unwrap()
            .is_none());
    }

    #[tokio::test]
    async fn stop_escalates_when_still_running() {
        let docker = RecordingDocker::ok(42000);
        docker.set_stop_leaves_running(true);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), &StubDocker::ok("1.0.0"))
            .await
            .unwrap();
        let locks = DeploymentLocks::new();
        ensure_workload(
            &docker,
            &node,
            &locks,
            WorkloadSpec {
                deployment_id: "sticky".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
            },
            Duration::from_secs(5),
            Duration::from_secs(1),
            OnConfigConflict::Recreate,
        )
        .await
        .unwrap();

        delete_workload(&docker, &locks, "sticky", Duration::from_secs(1))
            .await
            .unwrap();
        assert!(docker.stop_calls.load(Ordering::SeqCst) >= 1);
        assert!(docker.remove_calls.load(Ordering::SeqCst) >= 1);
        assert!(*docker.force_removed.lock().unwrap());
    }
}
