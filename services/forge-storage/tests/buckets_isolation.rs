//! Integration tests: bucket lifecycle + project isolation (13.02).

use forge_storage::api::validate::{validate_bucket_name, validate_object_key};
use forge_storage::app;
use forge_storage::backend::{LocalFsBackend, StorageBackend};
use forge_storage::config::{AuthMode, VerifyOnRead};
use forge_storage::meta::MetadataStore;
use forge_storage::state::{AppState, StorageMetrics};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn test_app(auth_mode: AuthMode) -> (tempfile::TempDir, axum::Router) {
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
        backend,
        ready: Arc::new(AtomicBool::new(true)),
        meta: Some(meta),
        auth_mode,
        identity: None,
        metrics: StorageMetrics::new(),
        meta_path,
        stream_buffer_bytes: forge_storage::backend::DEFAULT_STREAM_BUFFER_BYTES,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
    };
    (dir, app(state))
}

async fn request(
    app: axum::Router,
    method: &str,
    path: &str,
    project: Option<&str>,
    body: Option<&str>,
    auth: Option<&str>,
) -> (axum::http::StatusCode, Vec<u8>) {
    let mut builder = axum::http::Request::builder().method(method).uri(path);
    if let Some(p) = project {
        builder = builder.header("X-Forge-Project", p);
    }
    if let Some(a) = auth {
        builder = builder.header("Authorization", a);
    }
    if body.is_some() {
        builder = builder.header("content-type", "application/json");
    }
    let req = builder
        .body(axum::body::Body::from(body.unwrap_or("").to_string()))
        .unwrap();
    let response = app.oneshot(req).await.unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (status, bytes.to_vec())
}

#[tokio::test]
async fn create_list_isolation_delete_recreate() {
    let (_dir, app_a) = test_app(AuthMode::Dev).await;

    let (status, body) = request(
        app_a.clone(),
        "POST",
        "/v1/buckets",
        Some("proj-a"),
        Some(r#"{"name":"artifacts"}"#),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    let created: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(created["name"], "artifacts");
    assert_eq!(created["project_id"], "proj-a");

    let (status, body) = request(
        app_a.clone(),
        "GET",
        "/v1/buckets",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let listed: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(listed["buckets"].as_array().unwrap().len(), 1);

    // Project B cannot see or address project A's bucket (404 / empty).
    let (status, body) = request(
        app_a.clone(),
        "GET",
        "/v1/buckets",
        Some("proj-b"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let listed_b: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert!(listed_b["buckets"].as_array().unwrap().is_empty());

    let (status, _) = request(
        app_a.clone(),
        "GET",
        "/v1/buckets/artifacts",
        Some("proj-b"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);

    let (status, _) = request(
        app_a.clone(),
        "DELETE",
        "/v1/buckets/artifacts",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NO_CONTENT);

    let (status, _) = request(
        app_a,
        "POST",
        "/v1/buckets",
        Some("proj-a"),
        Some(r#"{"name":"artifacts"}"#),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
}

#[tokio::test]
async fn delete_non_empty_returns_409() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("storage");
    let backend = Arc::new(LocalFsBackend::new(&root, dir.path()));
    backend.init().await.expect("init");
    let meta_path = backend.meta_db_path();
    let meta = Arc::new(MetadataStore::open(&meta_path).expect("meta"));
    meta.create_bucket("proj-a", "artifacts").unwrap();
    meta.insert_object_placeholder("proj-a", "artifacts", "a.txt")
        .unwrap();

    let state = AppState {
        service_name: "forge-storage".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        backend,
        ready: Arc::new(AtomicBool::new(true)),
        meta: Some(meta),
        auth_mode: AuthMode::Dev,
        identity: None,
        metrics: StorageMetrics::new(),
        meta_path,
        stream_buffer_bytes: forge_storage::backend::DEFAULT_STREAM_BUFFER_BYTES,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: None,
        clock: forge_storage::signing::system_clock(),
    };
    let app = app(state);
    let (status, body) = request(
        app,
        "DELETE",
        "/v1/buckets/artifacts",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CONFLICT);
    let json: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(json["object_count"], 1);
}

#[tokio::test]
async fn enforce_mode_without_token_401() {
    let (_dir, app) = test_app(AuthMode::Enforce).await;
    let (status, body) = request(app, "GET", "/v1/buckets", Some("proj-a"), None, None).await;
    assert_eq!(status, axum::http::StatusCode::UNAUTHORIZED);
    let json: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(json["code"], "unauthenticated");
}

#[tokio::test]
async fn rejects_invalid_bucket_names() {
    let (_dir, app) = test_app(AuthMode::Dev).await;
    let (status, _) = request(
        app,
        "POST",
        "/v1/buckets",
        Some("proj-a"),
        Some(r#"{"name":"../etc"}"#),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::BAD_REQUEST);
}

#[test]
fn openapi_declares_bucket_paths() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-storage.openapi.yaml");
    // Docker image build context is services/forge-storage only — skip when absent.
    if !path.is_file() {
        assert!(validate_bucket_name("artifacts").is_ok());
        assert!(validate_object_key("path/to/obj").is_ok());
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("/v1/buckets"));
    assert!(doc.contains("/v1/buckets/{bucket}"));
    assert!(doc.contains("X-Forge-Project") || doc.contains("x-forge-project"));
    assert!(doc.contains("Bucket"));
    assert!(doc.contains("CreateBucketRequest"));
    assert!(validate_bucket_name("artifacts").is_ok());
    assert!(validate_object_key("path/to/obj").is_ok());
}
