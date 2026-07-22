use crate::health::AppState;
use crate::prober::note_workload_created;
use crate::workload::{self, WorkloadSpec};
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::Serialize;

#[derive(Debug, Serialize, PartialEq)]
pub struct ErrorBody {
    pub code: String,
    pub message: String,
}

#[derive(Debug, Serialize, PartialEq)]
pub struct ErrorEnvelope {
    pub error: ErrorBody,
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/workloads", post(handle_create))
        .route("/v1/workloads/{deployment_id}", get(handle_get))
}

async fn handle_create(
    State(state): State<AppState>,
    Json(spec): Json<WorkloadSpec>,
) -> impl IntoResponse {
    let container_port = spec.port;
    match workload::create_and_start(
        state.docker.as_ref(),
        state.node.as_ref(),
        spec,
        state.pull_timeout,
    )
    .await
    {
        Ok(view) => {
            note_workload_created(
                state.prober.cache().as_ref(),
                &view.deployment_id,
                view.host_port,
                container_port,
                &view.container_id,
            );
            (StatusCode::CREATED, Json(view)).into_response()
        }
        Err(err) => error_response(err).into_response(),
    }
}

async fn handle_get(
    State(state): State<AppState>,
    Path(deployment_id): Path<String>,
) -> impl IntoResponse {
    match workload::get_workload(state.docker.as_ref(), &deployment_id).await {
        Ok(view) => (StatusCode::OK, Json(view)).into_response(),
        Err(err) => error_response(err).into_response(),
    }
}

fn error_response(err: workload::WorkloadError) -> (StatusCode, Json<ErrorEnvelope>) {
    (
        err.status_code(),
        Json(ErrorEnvelope {
            error: ErrorBody {
                code: err.code().to_string(),
                message: err.message().to_string(),
            },
        }),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::RecordingDocker;
    use crate::docker::DockerEngine;
    use crate::heartbeat::Heartbeat;
    use crate::node::Node;
    use http_body_util::BodyExt;
    use std::sync::Arc;
    use std::time::Duration;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_app(docker: Arc<dyn DockerEngine>) -> Router {
        use crate::prober::{ProbeConfig, Prober, StatusCache};

        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), docker.as_ref()).await.unwrap();
        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker),
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .unwrap(),
        );
        let state = AppState {
            docker,
            node: Arc::new(node),
            heartbeat: Arc::new(Heartbeat::new()),
            pull_timeout: Duration::from_secs(30),
            prober,
            log_default_tail: 100,
            log_stream_buffer: 8192,
        };
        Router::new().merge(router()).with_state(state)
    }

    async fn json_request(
        app: Router,
        method: &str,
        path: &str,
        body: Option<serde_json::Value>,
    ) -> (StatusCode, serde_json::Value) {
        let builder = axum::http::Request::builder()
            .method(method)
            .uri(path)
            .header("content-type", "application/json");
        let request = match body {
            Some(v) => builder
                .body(axum::body::Body::from(serde_json::to_vec(&v).unwrap()))
                .unwrap(),
            None => builder.body(axum::body::Body::empty()).unwrap(),
        };
        let response = app.oneshot(request).await.unwrap();
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
    async fn post_creates_workload_contract_shape() {
        let docker = Arc::new(RecordingDocker::ok(49152));
        let app = test_app(docker).await;
        let (status, body) = json_request(
            app,
            "POST",
            "/v1/workloads",
            Some(serde_json::json!({
                "deployment_id": "deployment-123",
                "image": "localhost:5000/demo-go:latest",
                "port": 8080,
                "environment": {"FORGE_ENV": "development"}
            })),
        )
        .await;
        assert_eq!(status, StatusCode::CREATED);
        assert_eq!(body["deploymentId"], "deployment-123");
        assert!(!body["containerId"].as_str().unwrap().is_empty());
        assert_eq!(body["hostPort"], 49152);
        assert_eq!(body["state"], "starting");
    }

    #[tokio::test]
    async fn post_rejects_missing_image() {
        let docker = Arc::new(RecordingDocker::ok(1));
        let app = test_app(docker).await;
        let (status, body) = json_request(
            app,
            "POST",
            "/v1/workloads",
            Some(serde_json::json!({
                "deployment_id": "deployment-123",
                "image": "",
                "port": 8080,
                "environment": {}
            })),
        )
        .await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert_eq!(body["error"]["code"], "validation_error");
    }

    #[tokio::test]
    async fn post_pull_failure_is_bad_gateway() {
        let docker = Arc::new(RecordingDocker::fail_on("pull"));
        let app = test_app(docker).await;
        let (status, body) = json_request(
            app,
            "POST",
            "/v1/workloads",
            Some(serde_json::json!({
                "deployment_id": "deployment-123",
                "image": "localhost:5000/missing:latest",
                "port": 8080
            })),
        )
        .await;
        assert_eq!(status, StatusCode::BAD_GATEWAY);
        assert_eq!(body["error"]["code"], "image_pull_failed");
    }

    #[tokio::test]
    async fn get_returns_workload_after_create() {
        let docker: Arc<dyn DockerEngine> = Arc::new(RecordingDocker::ok(45555));
        let app = test_app(Arc::clone(&docker)).await;
        let (status, _) = json_request(
            app,
            "POST",
            "/v1/workloads",
            Some(serde_json::json!({
                "deployment_id": "deployment-123",
                "image": "localhost:5000/demo-go:latest",
                "port": 8080,
                "environment": {}
            })),
        )
        .await;
        assert_eq!(status, StatusCode::CREATED);

        let app = test_app(Arc::clone(&docker)).await;
        let (status, body) = json_request(app, "GET", "/v1/workloads/deployment-123", None).await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["hostPort"], 45555);
        assert_eq!(body["deploymentId"], "deployment-123");
        assert_eq!(body["state"], "starting");
    }

    #[tokio::test]
    async fn get_missing_workload_is_not_found() {
        let docker = Arc::new(RecordingDocker::missing());
        let app = test_app(docker).await;
        let (status, body) = json_request(app, "GET", "/v1/workloads/no-such", None).await;
        assert_eq!(status, StatusCode::NOT_FOUND);
        assert_eq!(body["error"]["code"], "not_found");
    }
}
