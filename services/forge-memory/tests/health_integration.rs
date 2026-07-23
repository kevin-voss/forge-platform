use forge_memory::app;
use forge_memory::state::AppState;
use forge_memory::store::{LocalStore, Store};
use http_body_util::BodyExt;
use std::fs;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn get(app: axum::Router, path: &str) -> (axum::http::StatusCode, serde_json::Value) {
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
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    (status, json)
}

#[tokio::test]
async fn ready_200_with_temp_writable_root() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let store = Arc::new(LocalStore::new(&root, dir.path()));
    store.init().await.expect("init");

    let state = AppState {
        service_name: "forge-memory".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        store,
        ready: Arc::new(AtomicBool::new(false)),
    };
    let app = app(state);
    let (status, body) = get(app, "/health/ready").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["status"], "ready");
}

#[tokio::test]
async fn ready_503_with_read_only_root() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("ro");
    fs::create_dir_all(&root).unwrap();
    // Block writes even for uid 0 (Docker build): vectors path is a file, not a dir.
    fs::write(root.join("vectors"), b"not-a-directory").unwrap();
    fs::create_dir_all(root.join("meta")).unwrap();

    let store = Arc::new(LocalStore::new(&root, dir.path()));
    let state = AppState {
        service_name: "forge-memory".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        store,
        ready: Arc::new(AtomicBool::new(true)),
    };
    let app = app(state);
    let (status, body) = get(app, "/health/ready").await;
    assert_eq!(status, axum::http::StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(body["status"], "not_ready");
}
