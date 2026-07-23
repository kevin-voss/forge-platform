//! Integration tests: quotas, delete + refcount GC, restart durability (13.06).

use forge_storage::app;
use forge_storage::backend::{LocalFsBackend, StorageBackend};
use forge_storage::config::{AuthMode, VerifyOnRead};
use forge_storage::integrity::sha256_hex;
use forge_storage::meta::MetadataStore;
use forge_storage::quota::DEFAULT_QUOTA_BYTES;
use forge_storage::state::{AppState, StorageMetrics};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn test_app_with_quota(
    default_quota_bytes: u64,
) -> (tempfile::TempDir, axum::Router, Arc<LocalFsBackend>, Arc<MetadataStore>) {
    let dir = tempdir().unwrap();
    let root = dir.path().join("storage");
    let backend = Arc::new(LocalFsBackend::new(&root, dir.path()));
    backend.init().await.expect("init");
    let meta_path = backend.meta_db_path();
    let meta = Arc::new(MetadataStore::open(&meta_path).expect("meta"));
    let state = AppState {
        service_name: "forge-storage".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        backend: backend.clone(),
        ready: Arc::new(AtomicBool::new(true)),
        meta: Some(meta.clone()),
        auth_mode: AuthMode::Dev,
        identity: None,
        metrics: StorageMetrics::new(),
        meta_path,
        stream_buffer_bytes: 4096,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
        default_quota_bytes,
    };
    (dir, app(state), backend, meta)
}

async fn create_bucket(app: &axum::Router, project: &str, name: &str) {
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/v1/buckets")
        .header("X-Forge-Project", project)
        .header("content-type", "application/json")
        .body(axum::body::Body::from(format!(r#"{{"name":"{name}"}}"#)))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::CREATED);
}

async fn put_bytes(
    app: &axum::Router,
    project: &str,
    bucket: &str,
    key: &str,
    body: Vec<u8>,
) -> axum::http::StatusCode {
    let uri = format!("/v1/buckets/{bucket}/objects/{key}");
    let req = axum::http::Request::builder()
        .method("PUT")
        .uri(&uri)
        .header("X-Forge-Project", project)
        .header("content-type", "application/octet-stream")
        .body(axum::body::Body::from(body))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    res.status()
}

async fn get_usage(app: &axum::Router, project: &str) -> (axum::http::StatusCode, serde_json::Value) {
    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/usage")
        .header("X-Forge-Project", project)
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    let status = res.status();
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    (status, json)
}

#[tokio::test]
async fn quota_rejects_then_delete_frees_space() {
    let (_dir, app, _, meta) = test_app_with_quota(20).await;
    meta.set_project_quota("proj-a", 20).unwrap();
    create_bucket(&app, "proj-a", "artifacts").await;

    let status = put_bytes(&app, "proj-a", "artifacts", "a.bin", vec![b'a'; 12]).await;
    assert_eq!(status, axum::http::StatusCode::CREATED);

    let status = put_bytes(&app, "proj-a", "artifacts", "b.bin", vec![b'b'; 12]).await;
    assert_eq!(status, axum::http::StatusCode::PAYLOAD_TOO_LARGE);

    let (status, usage) = get_usage(&app, "proj-a").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(usage["used_bytes"], 12);
    assert_eq!(usage["quota_bytes"], 20);
    assert_eq!(usage["objects"], 1);

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts/objects/a.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::NO_CONTENT);

    let status = put_bytes(&app, "proj-a", "artifacts", "b.bin", vec![b'b'; 12]).await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
}

#[tokio::test]
async fn delete_gcs_blob_only_when_unreferenced() {
    let (_dir, app, backend, _) = test_app_with_quota(DEFAULT_QUOTA_BYTES).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"shared-content-for-dedup".to_vec();
    let expect = sha256_hex(&payload);
    let storage_path = format!("{}/{}", &expect[..2], expect);

    assert_eq!(
        put_bytes(&app, "proj-a", "artifacts", "a.bin", payload.clone()).await,
        axum::http::StatusCode::CREATED
    );
    assert_eq!(
        put_bytes(&app, "proj-a", "artifacts", "b.bin", payload.clone()).await,
        axum::http::StatusCode::CREATED
    );
    assert!(backend.absolute_object_path(&storage_path).unwrap().is_file());

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts/objects/a.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NO_CONTENT
    );
    assert!(
        backend.absolute_object_path(&storage_path).unwrap().is_file(),
        "shared blob must remain"
    );

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts/objects/b.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NO_CONTENT
    );
    assert!(
        !backend.absolute_object_path(&storage_path).unwrap().exists(),
        "blob GC'd when last ref removed"
    );

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/a.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NOT_FOUND
    );
}

#[tokio::test]
async fn cascade_force_deletes_objects_and_bucket() {
    let (_dir, app, _, _) = test_app_with_quota(DEFAULT_QUOTA_BYTES).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    assert_eq!(
        put_bytes(&app, "proj-a", "artifacts", "one.bin", b"one".to_vec()).await,
        axum::http::StatusCode::CREATED
    );
    assert_eq!(
        put_bytes(&app, "proj-a", "artifacts", "two.bin", b"two".to_vec()).await,
        axum::http::StatusCode::CREATED
    );

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::CONFLICT
    );

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts?force=true")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NO_CONTENT
    );

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NOT_FOUND
    );
}

#[tokio::test]
async fn cross_project_delete_impossible() {
    let (_dir, app, _, _) = test_app_with_quota(DEFAULT_QUOTA_BYTES).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    assert_eq!(
        put_bytes(&app, "proj-a", "artifacts", "secret.bin", b"secret".to_vec()).await,
        axum::http::StatusCode::CREATED
    );

    let req = axum::http::Request::builder()
        .method("DELETE")
        .uri("/v1/buckets/artifacts/objects/secret.bin")
        .header("X-Forge-Project", "proj-b")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::NOT_FOUND
    );

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/secret.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    assert_eq!(
        app.clone().oneshot(req).await.unwrap().status(),
        axum::http::StatusCode::OK
    );
}

#[test]
fn openapi_documents_delete_cascade_usage_and_quota() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-storage.openapi.yaml");
    if !path.is_file() {
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("/v1/usage"));
    assert!(doc.contains("getProjectUsage") || doc.contains("operationId: getProjectUsage"));
    assert!(doc.contains("Usage"));
    assert!(doc.contains("deleteObject") || doc.contains("operationId: deleteObject"));
    assert!(doc.contains("force"));
    assert!(doc.contains("quota_exceeded"));
    assert!(doc.contains("\"413\"") || doc.contains("'413'") || doc.contains("413:"));
}

#[tokio::test]
async fn restart_reopen_preserves_objects_and_usage() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("storage");
    let backend = Arc::new(LocalFsBackend::new(&root, dir.path()));
    backend.init().await.expect("init");
    let meta_path = backend.meta_db_path();
    let meta = Arc::new(MetadataStore::open(&meta_path).expect("meta"));
    let state = AppState {
        service_name: "forge-storage".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        backend: backend.clone(),
        ready: Arc::new(AtomicBool::new(true)),
        meta: Some(meta),
        auth_mode: AuthMode::Dev,
        identity: None,
        metrics: StorageMetrics::new(),
        meta_path: meta_path.clone(),
        stream_buffer_bytes: 4096,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
        default_quota_bytes: DEFAULT_QUOTA_BYTES,
    };
    let app1 = app(state);
    create_bucket(&app1, "proj-a", "artifacts").await;
    let payload = b"durable-across-restart".to_vec();
    assert_eq!(
        put_bytes(&app1, "proj-a", "artifacts", "keep.bin", payload.clone()).await,
        axum::http::StatusCode::CREATED
    );
    let (status, usage_before) = get_usage(&app1, "proj-a").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(usage_before["used_bytes"], payload.len() as i64);
    drop(app1);

    // Simulate process restart: reopen SQLite + rebuild AppState on the same volume.
    let meta2 = Arc::new(MetadataStore::open(&meta_path).expect("reopen meta"));
    meta2.reconcile().expect("reconcile");
    let state2 = AppState {
        service_name: "forge-storage".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        backend: backend.clone(),
        ready: Arc::new(AtomicBool::new(true)),
        meta: Some(meta2),
        auth_mode: AuthMode::Dev,
        identity: None,
        metrics: StorageMetrics::new(),
        meta_path,
        stream_buffer_bytes: 4096,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
        default_quota_bytes: DEFAULT_QUOTA_BYTES,
    };
    let app2 = app(state2);

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/keep.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app2.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(bytes.as_ref(), payload.as_slice());

    let (status, usage_after) = get_usage(&app2, "proj-a").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(usage_after["used_bytes"], usage_before["used_bytes"]);
    assert_eq!(usage_after["objects"], 1);
    assert_eq!(usage_after["quota_bytes"], DEFAULT_QUOTA_BYTES as i64);
}
