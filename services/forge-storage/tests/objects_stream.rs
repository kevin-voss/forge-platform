//! Integration tests: streamed upload/download (13.03).

use bytes::Bytes;
use forge_storage::app;
use forge_storage::backend::{LocalFsBackend, StorageBackend, DEFAULT_STREAM_BUFFER_BYTES};
use forge_storage::config::{AuthMode, VerifyOnRead};
use forge_storage::meta::MetadataStore;
use forge_storage::state::{AppState, StorageMetrics};
use futures_util::stream;
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn test_app() -> (tempfile::TempDir, axum::Router, Arc<LocalFsBackend>) {
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
        meta_path,
        stream_buffer_bytes: 4096, // small buffer to exercise chunking
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
    };
    (dir, app(state), backend)
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
    content_type: &str,
) -> (axum::http::StatusCode, serde_json::Value) {
    let uri = format!("/v1/buckets/{bucket}/objects/{key}");
    let req = axum::http::Request::builder()
        .method("PUT")
        .uri(&uri)
        .header("X-Forge-Project", project)
        .header("content-type", content_type)
        .body(axum::body::Body::from(body))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    let status = res.status();
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap_or(serde_json::json!({}));
    (status, json)
}

async fn get_bytes(
    app: &axum::Router,
    project: &str,
    bucket: &str,
    key: &str,
) -> (axum::http::StatusCode, HeaderMapLite, Vec<u8>) {
    let uri = format!("/v1/buckets/{bucket}/objects/{key}");
    let req = axum::http::Request::builder()
        .method("GET")
        .uri(&uri)
        .header("X-Forge-Project", project)
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    let status = res.status();
    let ct = res
        .headers()
        .get(axum::http::header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_string();
    let cl = res
        .headers()
        .get(axum::http::header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_string();
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    (
        status,
        HeaderMapLite {
            content_type: ct,
            content_length: cl,
        },
        bytes.to_vec(),
    )
}

struct HeaderMapLite {
    content_type: String,
    content_length: String,
}

#[tokio::test]
async fn upload_download_round_trip_small_and_large() {
    let (_dir, app, backend) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;

    let small = b"hello-stream".to_vec();
    let (status, meta) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "small.txt",
        small.clone(),
        "text/plain",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(meta["size_bytes"], small.len() as i64);
    assert_eq!(meta["content_type"], "text/plain");
    assert!(!meta["storage_path"].as_str().unwrap().is_empty());

    let (status, headers, body) = get_bytes(&app, "proj-a", "artifacts", "small.txt").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(headers.content_type, "text/plain");
    assert_eq!(headers.content_length, small.len().to_string());
    assert_eq!(body, small);

    // Large fixture (> stream buffer) — byte-identical round trip.
    let large: Vec<u8> = (0..120_000u32).map(|i| (i % 251) as u8).collect();
    assert!(large.len() > 4096);
    let (status, meta) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "large.bin",
        large.clone(),
        "application/octet-stream",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(meta["size_bytes"], large.len() as i64);

    let (status, headers, body) = get_bytes(&app, "proj-a", "artifacts", "large.bin").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(headers.content_type, "application/octet-stream");
    assert_eq!(headers.content_length, large.len().to_string());
    assert_eq!(body, large);
    assert_eq!(backend.count_tmp_files().unwrap(), 0);
}

#[tokio::test]
async fn head_returns_metadata_without_body() {
    let (_dir, app, _) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "h.txt",
        b"abc".to_vec(),
        "text/plain",
    )
    .await;

    let req = axum::http::Request::builder()
        .method("HEAD")
        .uri("/v1/buckets/artifacts/objects/h.txt")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
    assert_eq!(
        res.headers()
            .get(axum::http::header::CONTENT_TYPE)
            .unwrap(),
        "text/plain"
    );
    assert_eq!(
        res.headers()
            .get(axum::http::header::CONTENT_LENGTH)
            .unwrap(),
        "3"
    );
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    assert!(bytes.is_empty());
}

#[tokio::test]
async fn overwrite_returns_latest_and_is_atomic() {
    let (_dir, app, _) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;

    let (status, _) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "obj.bin",
        b"version-one".to_vec(),
        "application/octet-stream",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);

    let (status, meta) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "obj.bin",
        b"version-two-longer".to_vec(),
        "application/octet-stream",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(meta["size_bytes"], 18);

    let (status, _, body) = get_bytes(&app, "proj-a", "artifacts", "obj.bin").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body, b"version-two-longer");
}

#[tokio::test]
async fn interrupted_upload_leaves_no_object_and_cleans_tmp() {
    let (_dir, app, backend) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;

    let chunks = vec![
        Ok::<_, std::io::Error>(Bytes::from(vec![1u8; 2048])),
        Err(std::io::Error::new(
            std::io::ErrorKind::BrokenPipe,
            "client disconnect",
        )),
    ];
    let body = axum::body::Body::from_stream(stream::iter(chunks));
    let req = axum::http::Request::builder()
        .method("PUT")
        .uri("/v1/buckets/artifacts/objects/partial.bin")
        .header("X-Forge-Project", "proj-a")
        .header("content-type", "application/octet-stream")
        .body(body)
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::INTERNAL_SERVER_ERROR);

    let (status, _, _) = get_bytes(&app, "proj-a", "artifacts", "partial.bin").await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);
    assert_eq!(backend.count_tmp_files().unwrap(), 0);
}

#[tokio::test]
async fn cross_project_object_is_404() {
    let (_dir, app, _) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "secret.txt",
        b"nope".to_vec(),
        "text/plain",
    )
    .await;

    let (status, _, _) = get_bytes(&app, "proj-b", "artifacts", "secret.txt").await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);

    let (status, _) = put_bytes(
        &app,
        "proj-b",
        "artifacts",
        "secret.txt",
        b"x".to_vec(),
        "text/plain",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn nested_object_key_round_trip() {
    let (_dir, app, _) = test_app().await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"nested-payload".to_vec();
    let (status, _) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "dir/sub/file.txt",
        payload.clone(),
        "text/plain",
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    let (status, _, body) = get_bytes(&app, "proj-a", "artifacts", "dir/sub/file.txt").await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body, payload);
}

#[test]
fn openapi_declares_object_put_get_head() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-storage.openapi.yaml");
    if !path.is_file() {
        assert_eq!(DEFAULT_STREAM_BUFFER_BYTES, 65_536);
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("/v1/buckets/{bucket}/objects/{key}"));
    assert!(doc.contains("putObject") || doc.contains("operationId: putObject"));
    assert!(doc.contains("getObject") || doc.contains("operationId: getObject"));
    assert!(doc.contains("headObject") || doc.contains("operationId: headObject"));
    assert!(doc.contains("application/octet-stream"));
    assert!(doc.contains("Content-Length") || doc.contains("content-length"));
    assert!(doc.contains("Content-Type") || doc.contains("content-type"));
}
