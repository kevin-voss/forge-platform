use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// Normalized workload status vocabulary (normative for Runtime / Control / Gateway).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum WorkloadStatus {
    Starting,
    Running,
    Ready,
    Unhealthy,
    Stopped,
    Failed,
}

impl WorkloadStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Starting => "starting",
            Self::Running => "running",
            Self::Ready => "ready",
            Self::Unhealthy => "unhealthy",
            Self::Stopped => "stopped",
            Self::Failed => "failed",
        }
    }
}

impl std::fmt::Display for WorkloadStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Map Runtime workload status → Control deployment status vocabulary.
///
/// Control statuses: `pending` | `active` | `failed` | `stopped`.
pub fn to_control_status(status: WorkloadStatus) -> &'static str {
    match status {
        WorkloadStatus::Ready | WorkloadStatus::Running => "active",
        WorkloadStatus::Failed | WorkloadStatus::Unhealthy => "failed",
        WorkloadStatus::Stopped => "stopped",
        WorkloadStatus::Starting => "pending",
    }
}

/// Coarse Docker container state used for derivation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DockerState {
    Created,
    Running,
    Restarting,
    Paused,
    Exited,
    Dead,
    Removing,
    Unknown,
}

impl DockerState {
    pub fn parse(raw: &str) -> Self {
        match raw.trim().to_ascii_lowercase().as_str() {
            "created" => Self::Created,
            "running" => Self::Running,
            "restarting" => Self::Restarting,
            "paused" => Self::Paused,
            "exited" => Self::Exited,
            "dead" => Self::Dead,
            "removing" => Self::Removing,
            _ => Self::Unknown,
        }
    }
}

/// Inputs for status derivation (docker × probe × threshold).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct DeriveInputs {
    pub docker_state: DockerState,
    pub live_ok: bool,
    pub ready_ok: bool,
    pub consecutive_live_failures: u32,
    pub failure_threshold: u32,
    pub stopped_by_operator: bool,
}

/// Derive normalized status from Docker state + probe results.
///
/// Rules (in priority order):
/// * operator stop → `stopped`
/// * exited/dead/error → `failed`
/// * paused/removing → `stopped`
/// * running & ready 200 → `ready`
/// * running & live 200 → `running`
/// * running & !live for ≥ threshold → `unhealthy`
/// * otherwise (just started / warming) → `starting`
pub fn derive_status(inputs: DeriveInputs) -> WorkloadStatus {
    if inputs.stopped_by_operator {
        return WorkloadStatus::Stopped;
    }

    match inputs.docker_state {
        DockerState::Exited | DockerState::Dead | DockerState::Unknown => {
            return WorkloadStatus::Failed;
        }
        DockerState::Paused | DockerState::Removing => {
            return WorkloadStatus::Stopped;
        }
        DockerState::Created | DockerState::Restarting => {
            return WorkloadStatus::Starting;
        }
        DockerState::Running => {}
    }

    if inputs.ready_ok {
        return WorkloadStatus::Ready;
    }
    if inputs.live_ok {
        return WorkloadStatus::Running;
    }
    if inputs.consecutive_live_failures >= inputs.failure_threshold.max(1) {
        return WorkloadStatus::Unhealthy;
    }
    WorkloadStatus::Starting
}

/// Last probe snapshot returned by the status API.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LastProbe {
    pub live: bool,
    pub ready: bool,
    pub at: DateTime<Utc>,
}

/// Status API response shape.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StatusView {
    pub deployment_id: String,
    pub status: WorkloadStatus,
    pub since: DateTime<Utc>,
    pub last_probe: LastProbe,
    pub restarts: u32,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn inputs(
        docker: DockerState,
        live: bool,
        ready: bool,
        failures: u32,
        threshold: u32,
        stopped: bool,
    ) -> DeriveInputs {
        DeriveInputs {
            docker_state: docker,
            live_ok: live,
            ready_ok: ready,
            consecutive_live_failures: failures,
            failure_threshold: threshold,
            stopped_by_operator: stopped,
        }
    }

    #[test]
    fn truth_table_core_transitions() {
        let cases = [
            (
                inputs(DockerState::Exited, false, false, 0, 3, false),
                WorkloadStatus::Failed,
            ),
            (
                inputs(DockerState::Dead, true, true, 0, 3, false),
                WorkloadStatus::Failed,
            ),
            (
                inputs(DockerState::Running, true, true, 0, 3, false),
                WorkloadStatus::Ready,
            ),
            (
                inputs(DockerState::Running, true, false, 0, 3, false),
                WorkloadStatus::Running,
            ),
            (
                inputs(DockerState::Running, false, false, 2, 3, false),
                WorkloadStatus::Starting,
            ),
            (
                inputs(DockerState::Running, false, false, 3, 3, false),
                WorkloadStatus::Unhealthy,
            ),
            (
                inputs(DockerState::Running, false, false, 5, 3, false),
                WorkloadStatus::Unhealthy,
            ),
            (
                inputs(DockerState::Created, false, false, 0, 3, false),
                WorkloadStatus::Starting,
            ),
            (
                inputs(DockerState::Restarting, false, false, 0, 3, false),
                WorkloadStatus::Starting,
            ),
            (
                inputs(DockerState::Paused, false, false, 0, 3, false),
                WorkloadStatus::Stopped,
            ),
            (
                inputs(DockerState::Running, true, true, 0, 3, true),
                WorkloadStatus::Stopped,
            ),
            (
                inputs(DockerState::Exited, false, false, 0, 3, true),
                WorkloadStatus::Stopped,
            ),
        ];

        for (i, (input, expected)) in cases.iter().enumerate() {
            assert_eq!(
                derive_status(*input),
                *expected,
                "case {i}: {input:?} → expected {expected}"
            );
        }
    }

    #[test]
    fn ready_requires_ready_probe_even_when_live() {
        let status = derive_status(inputs(DockerState::Running, true, false, 0, 3, false));
        assert_eq!(status, WorkloadStatus::Running);
        assert_ne!(status, WorkloadStatus::Ready);
    }

    #[test]
    fn failure_threshold_boundary() {
        let below = derive_status(inputs(DockerState::Running, false, false, 2, 3, false));
        let at = derive_status(inputs(DockerState::Running, false, false, 3, 3, false));
        assert_eq!(below, WorkloadStatus::Starting);
        assert_eq!(at, WorkloadStatus::Unhealthy);
    }

    #[test]
    fn status_serializes_snake_case() {
        let view = StatusView {
            deployment_id: "deployment-123".into(),
            status: WorkloadStatus::Ready,
            since: Utc::now(),
            last_probe: LastProbe {
                live: true,
                ready: true,
                at: Utc::now(),
            },
            restarts: 0,
        };
        let json = serde_json::to_value(&view).unwrap();
        assert_eq!(json["deploymentId"], "deployment-123");
        assert_eq!(json["status"], "ready");
        assert!(json["lastProbe"]["live"].as_bool().unwrap());
        assert!(json.get("deployment_id").is_none());
    }

    #[test]
    fn docker_state_parse() {
        assert_eq!(DockerState::parse("running"), DockerState::Running);
        assert_eq!(DockerState::parse("Exited"), DockerState::Exited);
        assert_eq!(DockerState::parse("weird"), DockerState::Unknown);
    }

    #[test]
    fn runtime_to_control_status_mapping() {
        let cases = [
            (WorkloadStatus::Starting, "pending"),
            (WorkloadStatus::Running, "active"),
            (WorkloadStatus::Ready, "active"),
            (WorkloadStatus::Unhealthy, "failed"),
            (WorkloadStatus::Failed, "failed"),
            (WorkloadStatus::Stopped, "stopped"),
        ];
        for (runtime, control) in cases {
            assert_eq!(to_control_status(runtime), control, "{runtime}");
        }
    }
}
