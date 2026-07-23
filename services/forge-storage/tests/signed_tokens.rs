//! Integration tests: HMAC signed access tokens + expiry (13.05).

use forge_storage::app;
use forge_storage::backend::{LocalFsBackend, StorageBackend};
use forge_storage::config::{AuthMode, VerifyOnRead};
use forge_storage::meta::MetadataStore;
use forge_storage::signing::{issue_token, system_clock, SigningKeys};
use forge_storage::state::{AppState, StorageMetrics};
use http_body_util::BodyExt;
use std::sync::atomic::{AtomicI64, AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

fn test_keys() -> SigningKeys {
    SigningKeys {
        key: b"integration-test-signing-key-aaaa".to_vec(),
        key_prev: None,
        max_ttl_seconds: 3600,
        clock_skew_seconds: 30,
    }
}

async fn test_app_with(
    keys: SigningKeys,
    clock: forge_storage::signing::Clock,
) -> (tempfile::TempDir, axum::Router, Arc<SigningKeys>) {
    let dir = tempdir().unwrap();
    let root = dir.path().join("storage");
    let backend = Arc::new(LocalFsBackend::new(&root, dir.path()));
    backend.init().await.expect("init");
    let meta_path = backend.meta_db_path();
    let meta = Arc::new(MetadataStore::open(&meta_path).expect("meta"));
    let signing = Arc::new(keys);
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
        stream_buffer_bytes: 4096,
        max_object_bytes: None,
        verify_on_read: VerifyOnRead::Off,
        signing: Some(signing.clone()),
        clock,
        default_quota_bytes: forge_storage::quota::DEFAULT_QUOTA_BYTES,
    };
    (dir, app(state), signing)
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

async fn put_object(app: &axum::Router, project: &str, body: &str) {
    let req = axum::http::Request::builder()
        .method("PUT")
        .uri("/v1/buckets/artifacts/objects/big.bin")
        .header("X-Forge-Project", project)
        .header("content-type", "application/octet-stream")
        .body(axum::body::Body::from(body.to_string()))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert!(
        res.status() == axum::http::StatusCode::CREATED
            || res.status() == axum::http::StatusCode::OK,
        "put status {}",
        res.status()
    );
}

async fn sign(
    app: &axum::Router,
    project: &str,
    method: &str,
    ttl: u64,
) -> (axum::http::StatusCode, serde_json::Value) {
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/v1/buckets/artifacts/objects/big.bin/sign")
        .header("X-Forge-Project", project)
        .header("content-type", "application/json")
        .body(axum::body::Body::from(format!(
            r#"{{"method":"{method}","ttl_seconds":{ttl}}}"#
        )))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    let status = res.status();
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap_or(serde_json::json!({}));
    (status, json)
}

#[tokio::test]
async fn issue_get_token_download_without_project_header() {
    let (_dir, app, _) = test_app_with(test_keys(), system_clock()).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_object(&app, "proj-a", "hello-signed").await;

    let (status, body) = sign(&app, "proj-a", "GET", 300).await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let token = body["token"].as_str().unwrap();
    assert!(body["url"].as_str().unwrap().contains("token="));
    assert!(body["expires_at"].as_str().is_some());

    let req = axum::http::Request::builder()
        .method("GET")
        .uri(format!("/v1/buckets/artifacts/objects/big.bin?token={token}"))
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(&bytes[..], b"hello-signed");
}

#[tokio::test]
async fn expired_token_returns_401_token_expired() {
    let now = Arc::new(AtomicI64::new(1_700_000_000));
    let clock_now = now.clone();
    let clock: forge_storage::signing::Clock =
        Arc::new(move || clock_now.load(Ordering::SeqCst));
    let (_dir, app, _) = test_app_with(test_keys(), clock).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_object(&app, "proj-a", "payload").await;

    let (status, body) = sign(&app, "proj-a", "GET", 2).await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let token = body["token"].as_str().unwrap().to_string();

    // Advance past exp + skew (2 + 30).
    now.store(1_700_000_000 + 2 + 30 + 1, Ordering::SeqCst);

    let req = axum::http::Request::builder()
        .method("GET")
        .uri(format!("/v1/buckets/artifacts/objects/big.bin?token={token}"))
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::UNAUTHORIZED);
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(json["code"], "token_expired");
}

#[tokio::test]
async fn get_token_cannot_put() {
    let (_dir, app, _) = test_app_with(test_keys(), system_clock()).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_object(&app, "proj-a", "v1").await;

    let (status, body) = sign(&app, "proj-a", "GET", 300).await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let token = body["token"].as_str().unwrap();

    let req = axum::http::Request::builder()
        .method("PUT")
        .uri(format!("/v1/buckets/artifacts/objects/big.bin?token={token}"))
        .header("content-type", "application/octet-stream")
        .body(axum::body::Body::from("v2"))
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::FORBIDDEN);
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(json["code"], "method_mismatch");
}

#[tokio::test]
async fn bearer_signed_token_works() {
    let (_dir, app, _) = test_app_with(test_keys(), system_clock()).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_object(&app, "proj-a", "via-bearer").await;

    let (status, body) = sign(&app, "proj-a", "GET", 300).await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let token = body["token"].as_str().unwrap();

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/big.bin")
        .header("Authorization", format!("Bearer {token}"))
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
}

#[tokio::test]
async fn ttl_over_max_rejected() {
    let mut keys = test_keys();
    keys.max_ttl_seconds = 60;
    let (_dir, app, _) = test_app_with(keys, system_clock()).await;
    create_bucket(&app, "proj-a", "artifacts").await;

    let (status, body) = sign(&app, "proj-a", "GET", 61).await;
    assert_eq!(status, axum::http::StatusCode::BAD_REQUEST);
    assert_eq!(body["code"], "ttl_too_large");
}

#[test]
fn openapi_documents_sign_endpoint_and_token_errors() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-storage.openapi.yaml");
    if !path.is_file() {
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("/v1/buckets/{bucket}/objects/{key}/sign"));
    assert!(doc.contains("signObjectAccess") || doc.contains("operationId: signObjectAccess"));
    assert!(doc.contains("SignObjectRequest"));
    assert!(doc.contains("SignObjectResponse"));
    assert!(doc.contains("token_expired"));
    assert!(doc.contains("invalid_token"));
    assert!(doc.contains("ttl_too_large"));
    assert!(doc.contains("SignedAccessToken") || doc.contains("name: token"));
}

#[tokio::test]
async fn previous_key_still_verifies() {
    let prev = b"previous-integration-key-bbbbbbbb".to_vec();
    let mut old = test_keys();
    old.key = prev.clone();
    let (token, _) = issue_token(
        &old,
        "GET",
        "proj-a",
        "artifacts",
        "big.bin",
        300,
        1_700_000_000,
    )
    .unwrap();

    let mut current = test_keys();
    current.key_prev = Some(prev);
    let now = Arc::new(AtomicI64::new(1_700_000_000));
    let clock_now = now.clone();
    let clock: forge_storage::signing::Clock =
        Arc::new(move || clock_now.load(Ordering::SeqCst));
    let (_dir, app, _) = test_app_with(current, clock).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_object(&app, "proj-a", "rotated").await;

    let req = axum::http::Request::builder()
        .method("GET")
        .uri(format!("/v1/buckets/artifacts/objects/big.bin?token={token}"))
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
}
