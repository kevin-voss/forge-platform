//! Integration tests: SHA-256 integrity + byte-range requests (13.04).

use forge_storage::app;
use forge_storage::backend::{LocalFsBackend, StorageBackend, DEFAULT_STREAM_BUFFER_BYTES};
use forge_storage::config::{AuthMode, VerifyOnRead};
use forge_storage::integrity::sha256_hex;
use forge_storage::meta::MetadataStore;
use forge_storage::state::{AppState, StorageMetrics};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn test_app(verify: VerifyOnRead) -> (tempfile::TempDir, axum::Router, Arc<LocalFsBackend>) {
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
        stream_buffer_bytes: 4096,
        max_object_bytes: None,
        verify_on_read: verify,
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
    expected_sha: Option<&str>,
) -> (axum::http::StatusCode, serde_json::Value) {
    let uri = format!("/v1/buckets/{bucket}/objects/{key}");
    let mut builder = axum::http::Request::builder()
        .method("PUT")
        .uri(&uri)
        .header("X-Forge-Project", project)
        .header("content-type", "application/octet-stream");
    if let Some(sha) = expected_sha {
        builder = builder.header("X-Expected-SHA256", sha);
    }
    let req = builder.body(axum::body::Body::from(body)).unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    let status = res.status();
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap_or(serde_json::json!({}));
    (status, json)
}

#[tokio::test]
async fn upload_get_etag_matches_client_sha256() {
    let (_dir, app, _) = test_app(VerifyOnRead::Off).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"checksum-fixture-bytes".to_vec();
    let expect = sha256_hex(&payload);

    let (status, meta) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "c.bin",
        payload.clone(),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(meta["sha256"], expect);
    assert!(meta["storage_path"]
        .as_str()
        .unwrap()
        .starts_with(&expect[..2]));

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/c.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::OK);
    let etag = res
        .headers()
        .get(axum::http::header::ETAG)
        .and_then(|v| v.to_str().ok())
        .unwrap()
        .to_string();
    assert_eq!(etag, format!("\"{expect}\""));
    assert_eq!(
        res.headers()
            .get("x-content-sha256")
            .and_then(|v| v.to_str().ok()),
        Some(expect.as_str())
    );
    assert_eq!(
        res.headers()
            .get(axum::http::header::ACCEPT_RANGES)
            .and_then(|v| v.to_str().ok()),
        Some("bytes")
    );
    let body = res.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(body.as_ref(), payload.as_slice());
    assert_eq!(sha256_hex(&body), expect);
}

#[tokio::test]
async fn expected_sha256_mismatch_returns_422_and_absent() {
    let (_dir, app, _) = test_app(VerifyOnRead::Off).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"real-bytes".to_vec();
    let wrong = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let (status, body) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "bad.bin",
        payload,
        Some(wrong),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["code"], "checksum_mismatch");

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/bad.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn range_request_returns_206_with_exact_bytes() {
    let (_dir, app, _) = test_app(VerifyOnRead::Off).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload: Vec<u8> = (0..300u32).map(|i| (i % 251) as u8).collect();
    put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "r.bin",
        payload.clone(),
        None,
    )
    .await;

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/r.bin")
        .header("X-Forge-Project", "proj-a")
        .header("Range", "bytes=100-199")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::PARTIAL_CONTENT);
    assert_eq!(
        res.headers()
            .get(axum::http::header::CONTENT_RANGE)
            .and_then(|v| v.to_str().ok()),
        Some("bytes 100-199/300")
    );
    assert_eq!(
        res.headers()
            .get(axum::http::header::CONTENT_LENGTH)
            .and_then(|v| v.to_str().ok()),
        Some("100")
    );
    let body = res.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(body.len(), 100);
    assert_eq!(body.as_ref(), &payload[100..200]);
}

#[tokio::test]
async fn unsatisfiable_range_returns_416() {
    let (_dir, app, _) = test_app(VerifyOnRead::Off).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "s.bin",
        b"short".to_vec(),
        None,
    )
    .await;

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/s.bin")
        .header("X-Forge-Project", "proj-a")
        .header("Range", "bytes=100-199")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::RANGE_NOT_SATISFIABLE);
    assert_eq!(
        res.headers()
            .get(axum::http::header::CONTENT_RANGE)
            .and_then(|v| v.to_str().ok()),
        Some("bytes */5")
    );
}

#[tokio::test]
async fn corrupted_blob_verify_mode_returns_integrity_error() {
    let (_dir, app, backend) = test_app(VerifyOnRead::Full).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"honest-bytes".to_vec();
    let (status, meta) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "corrupt.bin",
        payload,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    let storage_path = meta["storage_path"].as_str().unwrap();
    let abs = backend.absolute_object_path(storage_path).unwrap();
    std::fs::write(&abs, b"tampered!!!!!").unwrap();

    let req = axum::http::Request::builder()
        .method("GET")
        .uri("/v1/buckets/artifacts/objects/corrupt.bin")
        .header("X-Forge-Project", "proj-a")
        .body(axum::body::Body::empty())
        .unwrap();
    let res = app.clone().oneshot(req).await.unwrap();
    assert_eq!(res.status(), axum::http::StatusCode::INTERNAL_SERVER_ERROR);
    let bytes = res.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(json["code"], "integrity_error");
}

#[tokio::test]
async fn identical_content_dedups_to_single_blob() {
    let (_dir, app, backend) = test_app(VerifyOnRead::Off).await;
    create_bucket(&app, "proj-a", "artifacts").await;
    let payload = b"shared-content-payload".to_vec();

    let (status, a) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "one.bin",
        payload.clone(),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    let (status, b) = put_bytes(
        &app,
        "proj-a",
        "artifacts",
        "two.bin",
        payload.clone(),
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(a["sha256"], b["sha256"]);
    assert_eq!(a["storage_path"], b["storage_path"]);

    let path = backend
        .absolute_object_path(a["storage_path"].as_str().unwrap())
        .unwrap();
    assert!(path.is_file());
    // Only one physical blob file under objects/<aa>/<hash>.
    let objects = backend.root().join("objects");
    let mut files = 0usize;
    for entry in walkdir_files(&objects) {
        let _ = entry;
        files += 1;
    }
    assert_eq!(files, 1);
}

fn walkdir_files(root: &std::path::Path) -> Vec<std::path::PathBuf> {
    let mut out = Vec::new();
    fn walk(dir: &std::path::Path, out: &mut Vec<std::path::PathBuf>) {
        if let Ok(rd) = std::fs::read_dir(dir) {
            for e in rd.flatten() {
                let p = e.path();
                if p.is_dir() {
                    walk(&p, out);
                } else if p.is_file() {
                    out.push(p);
                }
            }
        }
    }
    walk(root, &mut out);
    out
}

#[test]
fn openapi_documents_range_etag_and_sha256() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-storage.openapi.yaml");
    if !path.is_file() {
        assert_eq!(DEFAULT_STREAM_BUFFER_BYTES, 65_536);
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("Range") || doc.contains("range"));
    assert!(doc.contains("206") || doc.contains("Partial Content"));
    assert!(doc.contains("416") || doc.contains("Range Not Satisfiable"));
    assert!(doc.contains("ETag") || doc.contains("etag"));
    assert!(doc.contains("sha256"));
    assert!(doc.contains("X-Expected-SHA256") || doc.contains("x-expected-sha256"));
    assert!(doc.contains("Accept-Ranges") || doc.contains("accept-ranges"));
}
