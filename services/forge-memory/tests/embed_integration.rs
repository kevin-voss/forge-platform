//! Integration: text upsert/query via ModelsClient; dim guard; models-down path.

use forge_memory::app;
use forge_memory::clients::{FakeModelsClient, ModelsClient};
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

fn app_state(
    root: &std::path::Path,
    base: &std::path::Path,
    models: Option<Arc<dyn ModelsClient>>,
) -> AppState {
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
        models,
        default_embed_model: "local-embed-small".into(),
        meta_path: root.join("meta/index.db"),
    }
}

async fn ready_app(
    root: &std::path::Path,
    base: &std::path::Path,
    models: Option<Arc<dyn ModelsClient>>,
) -> axum::Router {
    let store = Arc::new(LocalStore::new(root, base));
    store.init().await.unwrap();
    let state = app_state(root, base, models);
    state.ensure_collections().unwrap();
    app(state)
}

#[tokio::test]
async fn text_upsert_then_text_query_returns_neighbors() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let fake = FakeModelsClient::new(3);
    // Orient vectors so "database connection refused" is nearest to incident-db.
    fake.set_vector("db down", vec![1.0, 0.0, 0.0]);
    fake.set_vector("dns failure", vec![0.0, 1.0, 0.0]);
    fake.set_vector("database connection refused", vec![0.95, 0.05, 0.0]);

    let app = ready_app(
        &root,
        dir.path(),
        Some(fake.clone() as Arc<dyn ModelsClient>),
    )
    .await;

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
            "model": "local-embed-small",
            "items": [
                {"id":"incident-db","text":"db down","metadata":{"type":"deploy"}},
                {"id":"incident-dns","text":"dns failure","metadata":{"type":"net"}}
            ]
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK, "{body}");
    assert_eq!(body["upserted"], 2);
    assert_eq!(*fake.calls.lock().unwrap(), 1);

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({
            "text":"database connection refused",
            "model":"local-embed-small",
            "top_k": 2
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK, "{body}");
    let results = body["results"].as_array().unwrap();
    assert!(!results.is_empty());
    assert_eq!(results[0]["id"], "incident-db");
    assert!(results[0]["score"].as_f64().unwrap() > 0.9);
}

#[tokio::test]
async fn dim_mismatch_rejected_on_text_path() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let fake = FakeModelsClient::new(8); // collection dim will be 3
    let app = ready_app(&root, dir.path(), Some(fake as Arc<dyn ModelsClient>)).await;

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
        "/v1/collections/incidents/query",
        Some(serde_json::json!({
            "text":"anything",
            "model":"local-embed-small",
            "top_k": 1
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["code"], "dimension_mismatch");
}

#[tokio::test]
async fn models_down_errors_text_path_raw_vector_ok() {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let fake = FakeModelsClient::new(3);
    fake.set_unavailable();
    let app = ready_app(&root, dir.path(), Some(fake as Arc<dyn ModelsClient>)).await;

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
                {"id":"east","vector":[1.0,0.0,0.0],"metadata":{"type":"deploy"}}
            ]
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK, "{body}");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({"vector":[1.0,0.0,0.0],"top_k":1})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK, "{body}");
    assert_eq!(body["results"][0]["id"], "east");

    let (status, body) = json_request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query",
        Some(serde_json::json!({
            "text":"database connection refused",
            "model":"local-embed-small",
            "top_k": 1
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(body["code"], "embedding_backend_unavailable");
}
