use crate::health::AppState;
use crate::routes::workloads::{ErrorBody, ErrorEnvelope};
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/workloads/{deployment_id}/status", get(handle_status))
}

async fn handle_status(
    State(state): State<AppState>,
    Path(deployment_id): Path<String>,
) -> impl IntoResponse {
    let deployment_id = deployment_id.trim();
    if deployment_id.is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(ErrorEnvelope {
                error: ErrorBody {
                    code: "validation_error".into(),
                    message: "deployment_id is required".into(),
                },
            }),
        )
            .into_response();
    }

    match state.prober.status_for(deployment_id).await {
        Ok(view) => (StatusCode::OK, Json(view)).into_response(),
        Err(err) => {
            let not_found = err.contains("no workload") || err.contains("no managed workload");
            let status = if not_found {
                StatusCode::NOT_FOUND
            } else {
                StatusCode::INTERNAL_SERVER_ERROR
            };
            let code = if not_found {
                "not_found"
            } else {
                "status_unavailable"
            };
            (
                status,
                Json(ErrorEnvelope {
                    error: ErrorBody {
                        code: code.into(),
                        message: err,
                    },
                }),
            )
                .into_response()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::RecordingDocker;
    use crate::docker::DockerEngine;
    use crate::heartbeat::Heartbeat;
    use crate::node::Node;
    use crate::prober::{note_workload_created, ProbeConfig, Prober, StatusCache};
    use crate::status::WorkloadStatus;
    use crate::workload::{self, WorkloadSpec};
    use http_body_util::BodyExt;
    use std::collections::HashMap;
    use std::sync::Arc;
    use std::time::Duration;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_app(docker: Arc<dyn DockerEngine>) -> Router {
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), docker.as_ref()).await.unwrap();
        let cache = Arc::new(StatusCache::new());
        let prober =
            Arc::new(Prober::new(Arc::clone(&docker), cache, ProbeConfig::default()).unwrap());
        let state = AppState {
            docker,
            node: Arc::new(node),
            heartbeat: Arc::new(Heartbeat::new()),
            pull_timeout: Duration::from_secs(30),
            prober,
            log_default_tail: 100,
            log_stream_buffer: 8192,
            stop_grace: Duration::from_secs(10),
            on_config_conflict: crate::lifecycle::OnConfigConflict::Recreate,
            deployment_locks: Arc::new(crate::lifecycle::DeploymentLocks::new()),
        };
        Router::new()
            .merge(crate::routes::workloads::router())
            .merge(router())
            .with_state(state)
    }

    async fn get_json(app: Router, path: &str) -> (StatusCode, serde_json::Value) {
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri(path)
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let status = response.status();
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        let json: serde_json::Value = if bytes.is_empty() {
            serde_json::json!({})
        } else {
            serde_json::from_slice(&bytes).unwrap()
        };
        (status, json)
    }

    #[tokio::test]
    async fn status_contract_shape_after_create() {
        let docker: Arc<dyn DockerEngine> = Arc::new(RecordingDocker::ok(49152));
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), docker.as_ref()).await.unwrap();
        let cache = Arc::new(StatusCache::new());
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker),
                Arc::clone(&cache),
                ProbeConfig::default(),
            )
            .unwrap(),
        );
        let state = AppState {
            docker: Arc::clone(&docker),
            node: Arc::new(node),
            heartbeat: Arc::new(Heartbeat::new()),
            pull_timeout: Duration::from_secs(30),
            prober: Arc::clone(&prober),
            log_default_tail: 100,
            log_stream_buffer: 8192,
            stop_grace: Duration::from_secs(10),
            on_config_conflict: crate::lifecycle::OnConfigConflict::Recreate,
            deployment_locks: Arc::new(crate::lifecycle::DeploymentLocks::new()),
        };

        let view = workload::create_and_start(
            docker.as_ref(),
            state.node.as_ref(),
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: HashMap::new(),
                secrets_fingerprint: None,
            },
            Duration::from_secs(5),
        )
        .await
        .unwrap();
        note_workload_created(
            cache.as_ref(),
            &view.deployment_id,
            view.host_port,
            8080,
            &view.container_id,
        );

        let app = Router::new().merge(router()).with_state(state);
        let (status, body) = get_json(app, "/v1/workloads/deployment-123/status").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["deploymentId"], "deployment-123");
        assert!(body["status"].as_str().is_some());
        assert!(body.get("since").is_some());
        assert!(body.get("lastProbe").is_some());
        assert!(body["lastProbe"].get("live").is_some());
        assert!(body["lastProbe"].get("ready").is_some());
        assert!(body["lastProbe"].get("at").is_some());
        assert_eq!(body["restarts"], 0);
        // Contract uses camelCase keys only.
        assert!(body.get("deployment_id").is_none());
        assert!(body.get("last_probe").is_none());
        let _ = WorkloadStatus::Starting;
    }

    #[tokio::test]
    async fn status_missing_is_not_found() {
        let docker = Arc::new(RecordingDocker::missing());
        let app = test_app(docker).await;
        let (status, body) = get_json(app, "/v1/workloads/no-such/status").await;
        assert_eq!(status, StatusCode::NOT_FOUND);
        assert_eq!(body["error"]["code"], "not_found");
    }
}
