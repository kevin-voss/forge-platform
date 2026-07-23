//! Project-scope middleware: Identity bearer token or `X-Forge-Project` in dev mode.

use crate::config::AuthMode;
use crate::state::AppState;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use tracing::warn;

pub const HEADER_PROJECT: &str = "x-forge-project";

/// Project context attached to authorized storage requests.
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

fn unauthorized(msg: &str) -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(ErrorBody {
            error: msg.into(),
            code: Some("unauthenticated"),
        }),
    )
        .into_response()
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

pub fn parse_bearer(header: Option<&str>) -> Option<&str> {
    let header = header?.trim();
    let mut parts = header.splitn(2, char::is_whitespace);
    let scheme = parts.next()?;
    let token = parts.next()?.trim();
    if !scheme.eq_ignore_ascii_case("Bearer") || token.is_empty() {
        return None;
    }
    Some(token)
}

fn header_project(req: &Request<Body>) -> Option<String> {
    req.headers()
        .get(HEADER_PROJECT)
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_string)
}

/// Axum middleware: derive `ProjectContext` for `/v1/*` routes.
pub async fn require_project(
    State(state): State<AppState>,
    mut req: Request<Body>,
    next: Next,
) -> Response {
    let path = req.uri().path().to_string();
    let request_id = req
        .headers()
        .get("x-forge-request-id")
        .or_else(|| req.headers().get("x-request-id"))
        .and_then(|v| v.to_str().ok())
        .unwrap_or("-")
        .to_string();

    match state.auth_mode {
        AuthMode::Dev => {
            let Some(project_id) = header_project(&req) else {
                return bad_request("missing X-Forge-Project header");
            };
            warn!(
                path = %path,
                project_id = %project_id,
                request_id = %request_id,
                "FORGE_AUTH_MODE=dev — project scope from X-Forge-Project (insecure)"
            );
            req.extensions_mut().insert(ProjectContext { project_id });
            next.run(req).await
        }
        AuthMode::Enforce => {
            let auth_header = req
                .headers()
                .get(axum::http::header::AUTHORIZATION)
                .and_then(|v| v.to_str().ok())
                .map(|s| s.to_string());
            let Some(token) = parse_bearer(auth_header.as_deref()) else {
                return unauthorized("missing Authorization bearer token");
            };

            let Some(identity) = state.identity.clone() else {
                return unauthorized("identity unavailable");
            };

            let principal = match identity.introspect(token).await {
                Ok(p) if p.active => p,
                Ok(_) => return unauthorized("inactive token"),
                Err(_) => return unauthorized("invalid token"),
            };

            // Prefer scoped token project_id; else allow X-Forge-Project when membership lists it.
            let project_id = if let Some(pid) = principal
                .project_id
                .as_deref()
                .map(str::trim)
                .filter(|s| !s.is_empty())
            {
                pid.to_string()
            } else if let Some(header_pid) = header_project(&req) {
                if principal.allows_project(&header_pid) {
                    header_pid
                } else {
                    return unauthorized("project not permitted for token");
                }
            } else {
                return unauthorized("token has no project_id; provide X-Forge-Project");
            };

            req.extensions_mut().insert(ProjectContext { project_id });
            next.run(req).await
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_bearer_ok() {
        assert_eq!(parse_bearer(Some("Bearer abc")), Some("abc"));
        assert_eq!(parse_bearer(Some("bearer  xyz ")), Some("xyz"));
        assert!(parse_bearer(Some("Basic abc")).is_none());
        assert!(parse_bearer(None).is_none());
    }
}
