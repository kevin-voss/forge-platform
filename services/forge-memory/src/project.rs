//! Project-scope middleware via `X-Forge-Project` (dev header until 17.04 Identity).

use axum::body::Body;
use axum::http::{Request, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use tracing::warn;

pub const HEADER_PROJECT: &str = "x-forge-project";

/// Project context attached to authorized memory requests.
#[derive(Debug, Clone)]
pub struct ProjectContext {
    pub project_id: String,
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

fn bad_request(msg: &str) -> Response {
    (
        StatusCode::BAD_REQUEST,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("invalid_project"),
        }),
    )
        .into_response()
}

/// Axum middleware: require `X-Forge-Project` for `/v1/*` routes.
pub async fn require_project(mut req: Request<Body>, next: Next) -> Response {
    let path = req.uri().path().to_string();
    let request_id = req
        .headers()
        .get("x-forge-request-id")
        .or_else(|| req.headers().get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string();

    let project_id = req
        .headers()
        .get(HEADER_PROJECT)
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_string);

    let Some(project_id) = project_id else {
        return bad_request("missing X-Forge-Project header");
    };

    warn!(
        path = %path,
        project_id = %project_id,
        request_id = %request_id,
        "project scope from X-Forge-Project (dev; Identity ACL in 17.04)"
    );
    req.extensions_mut().insert(ProjectContext { project_id });
    next.run(req).await
}
