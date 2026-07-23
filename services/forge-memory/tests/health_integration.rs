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

fn base_state(store: Arc<LocalStore>, ready: bool) -> AppState {
    let meta_path = store.root().join("meta/index.db");
    AppState {
        service_name: "forge-memory".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        store,
        ready: Arc::new(AtomicBool::new(ready)),
        collections: Arc::new(std::sync::Mutex::new(None)),
        metrics: Arc::new(forge_memory::state::MemoryMetrics::default()),
        list_page_size: 100,
        max_dim: 4096,
        max_metadata_bytes: 65_536,
        max_top_k: 100,
        max_upsert_batch: 512,
        compact_on_boot: false,
        meta_path,
        auth_mode: forge_memory::config::AuthMode::Dev,
        identity: None,
        models: None,
        default_embed_model: "local-embed-small".into(),
    }
}

#[tokio::test]
async fn ready_200_with_temp_writable_root() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let store = Arc::new(LocalStore::new(&root, dir.path()));
    store.init().await.expect("init");

    let state = base_state(store, false);
    state.ensure_collections().expect("meta");
    state
        .ready
        .store(true, std::sync::atomic::Ordering::Relaxed);
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
    let state = base_state(store, true);
    let app = app(state);
    let (status, body) = get(app, "/health/ready").await;
    assert_eq!(status, axum::http::StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(body["status"], "not_ready");
}
