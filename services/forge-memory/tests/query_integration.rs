//! Integration: upsert, cosine NN query, filter, delete, restart persistence, caps.

use forge_memory::app;
use forge_memory::state::{AppState, MemoryMetrics};
use forge_memory::store::{LocalStore, Store};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

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
    AppState {
        service_name: "forge-memory".into(),
        service_version: "0.1.0".into(),
        started_at: Instant::now(),
        store: Arc::new(LocalStore::new(root, base)),
        ready: Arc::new(AtomicBool::new(true)),
        collections: Arc::new(std::sync::Mutex::new(None)),
        metrics: Arc::new(MemoryMetrics::default()),
        list_page_size: 100,
        max_dim: 4096,
        max_metadata_bytes: 65_536,
        max_top_k: 100,
        max_upsert_batch: 512,
        compact_on_boot: false,
        auth_mode: forge_memory::config::AuthMode::Dev,
        identity: None,
        meta_path: root.join("meta/index.db"),
    }
}

async fn ready_app(root: &std::path::Path, base: &std::path::Path) -> axum::Router {
    let store = Arc::new(LocalStore::new(root, base));
    store.init().await.unwrap();
    let state = app_state(root, base);
    state.ensure_collections().unwrap();
    app(state)
}

#[tokio::test]
async fn upsert_query_filter_delete_and_restart() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let app = ready_app(&root, dir.path()).await;

    let (status, _) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections",
        Some(serde_json::json!({"name":"incidents","dim":3,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/upsert",
        Some(serde_json::json!({
            "records": [
                {"id":"east","vector":[1.0,0.0,0.0],"metadata":{"type":"deploy"}},
                {"id":"north","vector":[0.0,1.0,0.0],"metadata":{"type":"alert"}},
                {"id":"diag","vector":[0.9,0.1,0.0],"metadata":{"type":"deploy"}}
            ]
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["upserted"], 3);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0,0.0],"top_k":2})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let results = body["results"].as_array().unwrap();
    assert_eq!(results.len(), 2);
    assert_eq!(results[0]["id"], "east");
    assert!((results[0]["score"].as_f64().unwrap() - 1.0).abs() < 1e-5);
    assert_eq!(results[1]["id"], "diag");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({
            "vector":[1.0,0.0,0.0],
            "top_k":5,
            "filter":{"type":"deploy"}
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let results = body["results"].as_array().unwrap();
    assert_eq!(results.len(), 2);
    assert!(results.iter().all(|r| r["metadata"]["type"] == "deploy"));

    let (status, _) = json_request(
        app.clone(),
        axum::http::Method::DELETE,
        "/v1/collections/incidents/records/diag",
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NO_CONTENT);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0,0.0],"top_k":5})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let results = body["results"].as_array().unwrap();
    assert_eq!(results.len(), 2);
    assert!(results.iter().all(|r| r["id"] != "diag"));

    // Dim mismatch / over-cap → 422
    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0],"top_k":1})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["code"], "dimension_mismatch");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0,0.0],"top_k":101})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["code"], "invalid");

    // Restart: reopen store; nearest ids unchanged for remaining vectors.
    let state = app_state(&root, dir.path());
    state.ensure_collections().unwrap();
    let restarted = forge_memory::app(state);
    let (status, body) = json_request(
        restarted,
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0,0.0],"top_k":1})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["results"][0]["id"], "east");
}

#[tokio::test]
async fn empty_collection_query_returns_empty() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let app = ready_app(&root, dir.path()).await;
    let (status, _) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections",
        Some(serde_json::json!({"name":"empty","dim":2,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    let (status, body) = json_request(
        app,
        axum::http::Method::POST,
        "/v1/collections/empty/query",
        Some(serde_json::json!({"vector":[1.0,0.0],"top_k":3})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["results"].as_array().unwrap().len(), 0);
}
