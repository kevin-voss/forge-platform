//! Integration: project isolation, namespaces, enforced auth.

use forge_memory::app;
use forge_memory::config::AuthMode;
use forge_memory::identity::{FixedIdentityClient, Principal};
use forge_memory::state::{AppState, MemoryMetrics};
use forge_memory::store::{LocalStore, Store};
use http_body_util::BodyExt;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;
use tower::ServiceExt;

async fn request(
    app: axum::Router,
    method: axum::http::Method,
    path: &str,
    project: Option<&str>,
    auth: Option<&str>,
    body: Option<serde_json::Value>,
) -> (axum::http::StatusCode, serde_json::Value) {
    let mut builder = axum::http::Request::builder().method(method).uri(path);
    if let Some(p) = project {
        builder = builder.header("x-forge-project", p);
    }
    if let Some(token) = auth {
        builder = builder.header("authorization", format!("Bearer {token}"));
    }
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
        serde_json::from_slice(&bytes).unwrap_or(serde_json::json!({}))
    };
    (status, json)
}

fn app_state(root: &std::path::Path, base: &std::path::Path, auth_mode: AuthMode) -> AppState {
    let identity = if auth_mode == AuthMode::Enforce {
        Some(Arc::new(FixedIdentityClient {
            principal: Principal {
                active: true,
                principal_type: Some("user".into()),
                principal_id: Some("u1".into()),
                project_id: Some("proj-a".into()),
                role: Some("developer".into()),
                memberships: None,
            },
        })
            as Arc<dyn forge_memory::identity::IdentityClient>)
    } else {
        None
    };
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
        meta_path: root.join("meta/index.db"),
        auth_mode,
        identity,
        models: None,
        default_embed_model: "local-embed-small".into(),
    }
}

async fn ready_app(auth_mode: AuthMode) -> (tempfile::TempDir, axum::Router) {
    let dir = tempdir().unwrap();
    let root = dir.path().join("memory");
    let store = Arc::new(LocalStore::new(&root, dir.path()));
    store.init().await.unwrap();
    let state = app_state(&root, dir.path(), auth_mode);
    state.ensure_collections().unwrap();
    (dir, app(state))
}

#[tokio::test]
async fn cross_project_collection_is_404() {
    let (_dir, app) = ready_app(AuthMode::Dev).await;
    let (status, _) = request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections",
        Some("proj-a"),
        None,
        Some(serde_json::json!({"name":"incidents","dim":3,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);

    let (status, body) = request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/incidents",
        Some("proj-b"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);
    assert_eq!(body["code"], "not_found");

    let (status, body) = request(
        app,
        axum::http::Method::GET,
        "/v1/collections",
        Some("proj-b"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["collections"].as_array().unwrap().len(), 0);
}

#[tokio::test]
async fn same_collection_name_independent_across_projects() {
    let (_dir, app) = ready_app(AuthMode::Dev).await;
    for (proj, dim) in [("proj-a", 3), ("proj-b", 8)] {
        let (status, body) = request(
            app.clone(),
            axum::http::Method::POST,
            "/v1/collections",
            Some(proj),
            None,
            Some(serde_json::json!({"name":"incidents","dim":dim,"distance":"cosine"})),
        )
        .await;
        assert_eq!(status, axum::http::StatusCode::CREATED);
        assert_eq!(body["dim"], dim);
        assert_eq!(body["project_id"], proj);
    }
    let (status, body) = request(
        app.clone(),
        axum::http::Method::GET,
        "/v1/collections/incidents",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["dim"], 3);
    let (status, body) = request(
        app,
        axum::http::Method::GET,
        "/v1/collections/incidents",
        Some("proj-b"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    assert_eq!(body["dim"], 8);
}

#[tokio::test]
async fn namespaces_isolate_records_in_queries() {
    let (_dir, app) = ready_app(AuthMode::Dev).await;
    for ns in ["agent-memory", "docs"] {
        let (status, _) = request(
            app.clone(),
            axum::http::Method::POST,
            &format!("/v1/collections?namespace={ns}"),
            Some("proj-a"),
            None,
            Some(serde_json::json!({"name":"incidents","dim":2,"distance":"cosine"})),
        )
        .await;
        assert_eq!(status, axum::http::StatusCode::CREATED);
    }
    let (status, _) = request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/upsert?namespace=agent-memory",
        Some("proj-a"),
        None,
        Some(serde_json::json!({
            "records":[{"id":"a1","vector":[1.0,0.0],"metadata":{"ns":"agent"}}]
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let (status, _) = request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/upsert?namespace=docs",
        Some("proj-a"),
        None,
        Some(serde_json::json!({
            "records":[{"id":"d1","vector":[0.0,1.0],"metadata":{"ns":"docs"}}]
        })),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);

    let (status, body) = request(
        app.clone(),
        axum::http::Method::POST,
        "/v1/collections/incidents/query?namespace=agent-memory",
        Some("proj-a"),
        None,
        Some(serde_json::json!({"vector":[1.0,0.0],"top_k":5})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::OK);
    let results = body["results"].as_array().unwrap();
    assert_eq!(results.len(), 1);
    assert_eq!(results[0]["id"], "a1");

    let (status, body) = request(
        app,
        axum::http::Method::GET,
        "/v1/collections/incidents/records/d1?namespace=agent-memory",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::NOT_FOUND);
    assert_eq!(body["code"], "not_found");
}

#[tokio::test]
async fn enforced_mode_without_token_401() {
    let (_dir, app) = ready_app(AuthMode::Enforce).await;
    let (status, body) = request(
        app,
        axum::http::Method::GET,
        "/v1/collections",
        Some("proj-a"),
        None,
        None,
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::UNAUTHORIZED);
    assert_eq!(body["code"], "unauthenticated");
}

#[tokio::test]
async fn enforced_mode_with_token_ok() {
    let (_dir, app) = ready_app(AuthMode::Enforce).await;
    let (status, body) = request(
        app,
        axum::http::Method::POST,
        "/v1/collections",
        None,
        Some("tok-a"),
        Some(serde_json::json!({"name":"incidents","dim":2,"distance":"cosine"})),
    )
    .await;
    assert_eq!(status, axum::http::StatusCode::CREATED);
    assert_eq!(body["project_id"], "proj-a");
}

#[test]
fn openapi_documents_auth_and_namespace() {
    let root = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let path = root.join("../../contracts/openapi/forge-memory.openapi.yaml");
    if !path.is_file() {
        return;
    }
    let doc = std::fs::read_to_string(&path).expect("openapi");
    assert!(doc.contains("namespace"));
    assert!(doc.contains("bearerAuth") || doc.contains("Authorization"));
    assert!(doc.contains("X-Forge-Project"));
    assert!(doc.contains("401") || doc.contains("\"401\""));
}
