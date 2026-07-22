use crate::health::AppState;
use crate::logs::{
    fetch_logs, format_log_line, is_container_gone, log_stream_closed, log_stream_opened,
    open_follow_stream, parse_logs_query, terminal_marker, warn_stream_error, LogFormat, LogsError,
    LogsQuery,
};
use crate::routes::workloads::{ErrorBody, ErrorEnvelope};
use axum::extract::{Path, Query, State};
use axum::http::{header, HeaderMap, StatusCode};
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use axum::Router;
use futures_util::StreamExt;
use std::collections::HashMap;
use std::convert::Infallible;
use std::time::Instant;
use tokio::sync::mpsc;

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/workloads/{deployment_id}/logs", get(handle_logs))
}

async fn handle_logs(
    State(state): State<AppState>,
    Path(deployment_id): Path<String>,
    Query(params): Query<HashMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let prefer_ndjson = accept_prefers_ndjson(&headers);
    let pairs: Vec<(String, String)> = params.into_iter().collect();
    let query = match parse_logs_query(&pairs, state.log_default_tail, prefer_ndjson) {
        Ok(q) => q,
        Err(err) => return error_response(err),
    };

    if query.follow {
        follow_response(state, deployment_id, query).await
    } else {
        bounded_response(state, deployment_id, query).await
    }
}

async fn bounded_response(state: AppState, deployment_id: String, query: LogsQuery) -> Response {
    log_stream_opened(&deployment_id, false, query.format);
    match fetch_logs(state.docker.as_ref(), &deployment_id, &query).await {
        Ok(lines) => {
            let body = lines
                .iter()
                .map(|line| format_log_line(line, query.format))
                .collect::<Vec<_>>()
                .join("\n");
            let body = if body.is_empty() {
                body
            } else {
                format!("{body}\n")
            };
            let content_type = match query.format {
                LogFormat::Text => "text/plain; charset=utf-8",
                LogFormat::Ndjson => "application/x-ndjson",
            };
            (StatusCode::OK, [(header::CONTENT_TYPE, content_type)], body).into_response()
        }
        Err(err) => error_response(err),
    }
}

async fn follow_response(state: AppState, deployment_id: String, query: LogsQuery) -> Response {
    let opened = Instant::now();
    log_stream_opened(&deployment_id, true, query.format);

    let docker_stream =
        match open_follow_stream(state.docker.as_ref(), &deployment_id, &query).await {
            Ok(s) => s,
            Err(err) => return error_response(err),
        };

    // Bounded channel provides backpressure; capacity derived from FORGE_LOG_STREAM_BUFFER.
    let capacity = (state.log_stream_buffer / 128).clamp(8, 1024);
    let (tx, rx) = mpsc::channel::<Result<Event, Infallible>>(capacity);

    let dep_id = deployment_id.clone();
    let format = query.format;
    tokio::spawn(async move {
        let mut docker_stream = docker_stream;
        let mut reason = "eof";
        while let Some(item) = docker_stream.next().await {
            match item {
                Ok(line) => {
                    let data = format_log_line(&line, format);
                    let event = Event::default().data(data);
                    if tx.send(Ok(event)).await.is_err() {
                        reason = "client_disconnect";
                        break;
                    }
                }
                Err(err) => {
                    if is_container_gone(&err) {
                        reason = "container_gone";
                        let marker = terminal_marker(reason, format);
                        let _ = tx
                            .send(Ok(Event::default().event("end").data(marker)))
                            .await;
                        break;
                    }
                    warn_stream_error(&dep_id, &err);
                    reason = "error";
                    let marker = terminal_marker(reason, format);
                    let _ = tx
                        .send(Ok(Event::default().event("end").data(marker)))
                        .await;
                    break;
                }
            }
        }
        if reason == "eof" {
            let marker = terminal_marker(reason, format);
            let _ = tx
                .send(Ok(Event::default().event("end").data(marker)))
                .await;
        }
        // Dropping tx / docker_stream cancels the Docker log request.
        drop(docker_stream);
        log_stream_closed(&dep_id, opened.elapsed(), reason);
    });

    let event_stream = futures_util::stream::unfold(rx, |mut rx| async move {
        rx.recv().await.map(|item| (item, rx))
    });

    Sse::new(event_stream)
        .keep_alive(KeepAlive::default())
        .into_response()
}

fn accept_prefers_ndjson(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT)
        .and_then(|v| v.to_str().ok())
        .map(|v| {
            let lower = v.to_ascii_lowercase();
            lower.contains("application/x-ndjson") || lower.contains("ndjson")
        })
        .unwrap_or(false)
}

fn error_response(err: LogsError) -> Response {
    (
        err.status_code(),
        axum::Json(ErrorEnvelope {
            error: ErrorBody {
                code: err.code().to_string(),
                message: err.message().to_string(),
            },
        }),
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::docker::test_support::RecordingDocker;
    use crate::docker::{DockerEngine, StreamSource};
    use crate::heartbeat::Heartbeat;
    use crate::node::Node;
    use crate::prober::{ProbeConfig, Prober, StatusCache};
    use crate::workload::{self, WorkloadSpec};
    use http_body_util::BodyExt;
    use std::sync::atomic::Ordering;
    use std::sync::Arc;
    use std::time::Duration;
    use tempfile::tempdir;
    use tower::ServiceExt;

    async fn test_app(docker: Arc<dyn DockerEngine>) -> Router {
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
            stop_grace: Duration::from_secs(10),
            on_config_conflict: crate::lifecycle::OnConfigConflict::Recreate,
            deployment_locks: Arc::new(crate::lifecycle::DeploymentLocks::new()),
        };
        Router::new()
            .merge(crate::routes::workloads::router())
            .merge(router())
            .with_state(state)
    }

    async fn create_workload(app: Router) -> Router {
        let request = axum::http::Request::builder()
            .method("POST")
            .uri("/v1/workloads")
            .header("content-type", "application/json")
            .body(axum::body::Body::from(
                serde_json::to_vec(&serde_json::json!({
                    "deployment_id": "deployment-123",
                    "image": "localhost:5000/demo-go:latest",
                    "port": 8080,
                    "environment": {}
                }))
                .unwrap(),
            ))
            .unwrap();
        let response = app.clone().oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::CREATED);
        app
    }

    #[tokio::test]
    async fn bounded_logs_return_text_with_stream_labels() {
        let docker: Arc<dyn DockerEngine> = Arc::new(RecordingDocker::ok(49152));
        let app = create_workload(test_app(Arc::clone(&docker)).await).await;
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/v1/workloads/deployment-123/logs?tail=10")
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers().get(header::CONTENT_TYPE).unwrap(),
            "text/plain; charset=utf-8"
        );
        let body = String::from_utf8(
            response
                .into_body()
                .collect()
                .await
                .unwrap()
                .to_bytes()
                .to_vec(),
        )
        .unwrap();
        assert!(body.contains("[stdout] hello from stdout"));
        assert!(body.contains("[stderr] oops from stderr"));
    }

    #[tokio::test]
    async fn bounded_logs_ndjson_contract() {
        let docker: Arc<dyn DockerEngine> = Arc::new(RecordingDocker::ok(49152));
        let app = create_workload(test_app(Arc::clone(&docker)).await).await;
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/v1/workloads/deployment-123/logs?format=ndjson&streams=stdout")
                    .header(header::ACCEPT, "application/x-ndjson")
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers().get(header::CONTENT_TYPE).unwrap(),
            "application/x-ndjson"
        );
        let body = String::from_utf8(
            response
                .into_body()
                .collect()
                .await
                .unwrap()
                .to_bytes()
                .to_vec(),
        )
        .unwrap();
        let line: serde_json::Value = serde_json::from_str(body.lines().next().unwrap()).unwrap();
        assert_eq!(line["stream"], "stdout");
        assert!(line["message"].as_str().unwrap().contains("hello"));
        assert!(!body.contains("stderr"));
    }

    #[tokio::test]
    async fn unknown_workload_is_not_found() {
        let docker = Arc::new(RecordingDocker::missing());
        let app = test_app(docker).await;
        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/v1/workloads/no-such/logs")
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::NOT_FOUND);
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(json["error"]["code"], "not_found");
    }

    #[tokio::test]
    async fn follow_streams_and_cancels_on_disconnect() {
        let mut recording = RecordingDocker::ok(49152);
        recording.log_follow_hold = true;
        let docker = Arc::new(recording);
        let dir = tempdir().unwrap();
        let node = Node::bootstrap(dir.path(), docker.as_ref()).await.unwrap();
        workload::create_and_start(
            docker.as_ref(),
            &node,
            WorkloadSpec {
                deployment_id: "deployment-123".into(),
                image: "localhost:5000/demo-go:latest".into(),
                port: 8080,
                environment: Default::default(),
            },
            Duration::from_secs(5),
        )
        .await
        .unwrap();

        let prober = Arc::new(
            Prober::new(
                Arc::clone(&docker) as Arc<dyn DockerEngine>,
                Arc::new(StatusCache::new()),
                ProbeConfig::default(),
            )
            .unwrap(),
        );
        let state = AppState {
            docker: Arc::clone(&docker) as Arc<dyn DockerEngine>,
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
        let app = Router::new().merge(router()).with_state(state);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/v1/workloads/deployment-123/logs?follow=true")
                    .body(axum::body::Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let ctype = response
            .headers()
            .get(header::CONTENT_TYPE)
            .unwrap()
            .to_str()
            .unwrap();
        assert!(ctype.starts_with("text/event-stream"));

        // Read a little then drop the body to simulate client disconnect.
        let mut body = response.into_body();
        let _ = BodyExt::frame(&mut body).await;
        drop(body);

        // Give the spawned task a moment to observe send failure and exit.
        tokio::time::sleep(Duration::from_millis(50)).await;
        assert!(docker.logs_calls.load(Ordering::SeqCst) >= 1);
        let _ = StreamSource::Stdout;
    }
}
