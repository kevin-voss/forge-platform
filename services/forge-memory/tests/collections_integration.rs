//! Integration: collection CRUD, record persist across reopen, dim mismatch.

use forge_memory::app;
use forge_memory::collections::CollectionStore;
use forge_memory::meta::MetaStore;
use forge_memory::state::{AppState, MemoryMetrics};
use forge_memory::store::{LocalStore, Store};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

fn project_headers() -> axum::http::HeaderMap {
    let mut h = axum::http::HeaderMap::new();
    h.insert("x-forge-project", "proj-a".parse().unwrap());
    h
}

async fn json_request(
    app: axum::Router,
    method: axum::http::Method,
    path: &str,
    body: Option<serde_json::Value>,
) -> (axum::http::StatusCode, serde_json::Value) {
    let mut builder = axum::http::Request::builder()
        .method(method)
        .uri(path)
        .header("x-forge-project", "proj-a");
    let req = if let Some(b) = body {
        builder = builder.header("content-type", "application/json");
        builder
            .body(axum::body::Body::from(serde_json::to_vec(&b).unwrap()))
            .unwrap()
    } else {
        builder.body(axum::body::Body::empty()).unwrap()
    };
    let response = app.oneshot(req).await.unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    let json: serde_json::Value = if bytes.is_empty() {
        serde_json::json!({})
    } else {
        serde_json::from_slice(&bytes).unwrap()
    };
    (status, json)
}

fn app_state(root: &std::path::Path, base: &std::path::Path) -> AppState {
    let store = Arc::new(LocalStore::new(root, base));
    let meta_path = root.join("meta/index.db");
    let state = AppState {
        service_name: "forge-memory".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        store,
        ready: Arc::new(AtomicBool::new(true)),
        collections: Arc::new(std::sync::Mutex::new(None)),
        metrics: Arc::new(MemoryMetrics::default()),
        list_page_size: 100,
        max_dim: 4096,
        max_metadata_bytes: 65_536,
        meta_path,
    };
    state
}

#[tokio::test]
async fn collection_crud_duplicate_and_missing() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let store = Arc::new(LocalStore::new(&root, dir.path()));
    store.init().await.unwrap();
    let state = app_state(&root, dir.path());
    state.ensure_collections().unwrap();
    let app = app(state);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections",
        Some(serde_json::json!({"name":"incidents","dim":384,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(body["dim"], 384);
    assert_eq!(body["distance"], "cosine");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/incidents",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["dim"], 384);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections",
        Some(serde_json::json!({"name":"incidents","dim":384,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CONFLICT);
    assert_eq!(body["code"], "conflict");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/missing",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);
    assert_eq!(body["code"], "not_found");

    let (status, _) = json_request(
        app,
        axum::http::Method::DELETE,
        "/v1/collections/incidents",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NO_CONTENT);
    let _ = project_headers();
}

#[tokio::test]
async fn record_persist_restart_and_wrong_dim() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let store = Arc::new(LocalStore::new(&root, dir.path()));
    store.init().await.unwrap();
    let meta_path = root.join("meta/index.db");
    let vectors = root.join("vectors");

    {
        let meta = Arc::new(MetaStore::open(&meta_path).unwrap());
        let cs = CollectionStore::new(Arc::clone(&meta), vectors.clone(), 4096, 65_536);
        cs.create_collection("proj-a", "incidents", 384, "cosine")
            .unwrap();
        let mut vec = vec![0.0_f32; 384];
        vec[0] = 1.0;
        vec[1] = 0.5;
        cs.insert_record(
            "proj-a",
            "incidents",
            "rec-1",
            &vec,
            serde_json::json!({"type":"deploy","sev":1}),
            None,
        )
        .unwrap();
        let err = cs
            .insert_record(
                "proj-a",
                "incidents",
                "bad",
                &[1.0, 2.0],
                serde_json::json!({}),
                None,
            )
            .unwrap_err();
        assert!(matches!(
            err,
            forge_memory::collections::CollectionError::DimensionMismatch {
                expected: 384,
                got: 2
            }
        ));
    }

    // Simulate restart: reopen meta + vectors, serve HTTP get.
    let state = app_state(&root, dir.path());
    state.ensure_collections().unwrap();
    let app = app(state);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/incidents",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["dim"], 384);
    assert_eq!(body["count"], 1);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/incidents/records/rec-1",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["id"], "rec-1");
    assert_eq!(body["metadata"]["type"], "deploy");
    let vector = body["vector"].as_array().unwrap();
    assert_eq!(vector.len(), 384);
    assert!((vector[0].as_f64().unwrap() - 1.0).abs() < 1e-6);
    assert!((vector[1].as_f64().unwrap() - 0.5).abs() < 1e-6);

    let (status, body) = json_request(
        app,
        axum::http::Method::GET,
        "/v1/collections/incidents/records?limit=10",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["records"].as_array().unwrap().len(), 1);
}
