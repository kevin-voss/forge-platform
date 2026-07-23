//! Project-scope middleware: Identity bearer or `X-Forge-Project` (dev).

use crate::acl::{self, AclDeny};
use crate::config::AuthMode;
use crate::state::AppState;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use tracing::{info, warn};

pub const HEADER_PROJECT: &str = "x-forge-project";
pub const DEFAULT_NAMESPACE: &str = "";

/// Project + optional namespace context attached to authorized memory requests.
#[derive(Debug, Clone)]
pub struct ProjectContext {
    pub project_id: String,
    pub namespace: String,
    pub role: Option<String>,
    pub auth_source: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AccessKind {
    Read,
    Write,
}

impl AccessKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Read => "read",
            Self::Write => "write",
        }
    }

    pub fn from_method(method: &axum::http::Method) -> Self {
        if method == axum::http::Method::GET || method == axum::http::Method::HEAD {
            Self::Read
        } else {
            Self::Write
        }
    }
}

#[derive(Debug, Serialize)]
struct ErrorBody {
    error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

fn unauthorized(msg: &str, code: &'static str) -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(ErrorBody {
            error: msg.into(),
            code: Some(code),
        }),
    )
        .into_response()
}

fn forbidden(msg: &str, code: &'static str) -> Response {
    (
        StatusCode::FORBIDDEN,
        Json(ErrorBody {
            error: msg.into(),
            code: Some(code),
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

/// Extract `namespace` query param (missing → default empty namespace).
pub fn namespace_from_query(query: Option<&str>) -> String {
    let Some(query) = query else {
        return DEFAULT_NAMESPACE.to_string();
    };
    for pair in query.split('&') {
        let mut kv = pair.splitn(2, '=');
        let k = kv.next().unwrap_or("");
        if k != "namespace" {
            continue;
        }
        let v = kv.next().unwrap_or("");
        return percent_decode(v);
    }
    DEFAULT_NAMESPACE.to_string()
}

fn percent_decode(raw: &str) -> String {
    let bytes = raw.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'%' if i + 2 < bytes.len() => {
                let h = || -> Option<u8> {
                    let hi = from_hex(bytes[i + 1])?;
                    let lo = from_hex(bytes[i + 2])?;
                    Some((hi << 4) | lo)
                };
                if let Some(b) = h() {
                    out.push(b);
                    i += 3;
                    continue;
                }
                out.push(bytes[i]);
                i += 1;
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            b => {
                out.push(b);
                i += 1;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn from_hex(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

/// Normalize / validate optional sub-namespace (`""` allowed = project default).
pub fn normalize_namespace(raw: &str) -> Result<String, String> {
    let ns = raw.trim();
    if ns.is_empty() {
        return Ok(DEFAULT_NAMESPACE.to_string());
    }
    if ns.len() > 128 {
        return Err("namespace must be at most 128 characters".into());
    }
    if ns.contains('\0') || ns.contains('/') || ns.contains('\\') || ns.contains("..") {
        return Err("namespace contains illegal characters".into());
    }
    if !ns
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '_')
    {
        return Err("namespace may only contain a-z, 0-9, '-', and '_'".into());
    }
    let bytes = ns.as_bytes();
    if !bytes[0].is_ascii_alphanumeric() || !bytes[bytes.len() - 1].is_ascii_alphanumeric() {
        return Err("namespace must start and end with a letter or digit".into());
    }
    Ok(ns.to_string())
}

/// Axum middleware: derive `ProjectContext` + ACL for `/v1/*` routes.
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

    let namespace = match normalize_namespace(&namespace_from_query(req.uri().query())) {
        Ok(ns) => ns,
        Err(msg) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(ErrorBody {
                    error: msg,
                    code: Some("invalid"),
                }),
            )
                .into_response();
        }
    };

    let access = AccessKind::from_method(req.method());

    let ctx = match state.auth_mode {
        AuthMode::Dev => {
            let Some(project_id) = header_project(&req) else {
                return bad_request("missing X-Forge-Project header");
            };
            warn!(
                path = %path,
                project_id = %project_id,
                namespace = %namespace,
                request_id = %request_id,
                "FORGE_AUTH_MODE=dev — project scope from X-Forge-Project (insecure)"
            );
            ProjectContext {
                project_id,
                namespace: namespace.clone(),
                role: None,
                auth_source: "dev_header".into(),
            }
        }
        AuthMode::Enforce => {
            let auth_header = req
                .headers()
                .get(axum::http::header::AUTHORIZATION)
                .and_then(|v| v.to_str().ok())
                .map(|s| s.to_string());
            let Some(token) = parse_bearer(auth_header.as_deref()) else {
                return unauthorized("missing Authorization bearer token", "unauthenticated");
            };

            let Some(identity) = state.identity.clone() else {
                return unauthorized("identity unavailable", "unauthenticated");
            };

            let principal = match identity.introspect(token).await {
                Ok(p) if p.active => p,
                Ok(_) => return unauthorized("inactive token", "unauthenticated"),
                Err(_) => return unauthorized("invalid token", "unauthenticated"),
            };

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
                    return unauthorized("project not permitted for token", "unauthenticated");
                }
            } else {
                return unauthorized(
                    "token has no project_id; provide X-Forge-Project",
                    "unauthenticated",
                );
            };

            let role = principal.role_for_project(&project_id);
            info!(
                path = %path,
                project_id = %project_id,
                namespace = %namespace,
                role = role.as_deref().unwrap_or("-"),
                request_id = %request_id,
                "project scope from Identity token"
            );
            ProjectContext {
                project_id,
                namespace: namespace.clone(),
                role,
                auth_source: "identity".into(),
            }
        }
    };

    if let Err(deny) = acl::check_access(&ctx, access) {
        acl::deny(&state, &ctx, access, &deny);
        let AclDeny { reason, message } = deny;
        return forbidden(&message, reason);
    }
    acl::audit_allow(&ctx, access);

    info!(
        project_id = %ctx.project_id,
        namespace = %ctx.namespace,
        access = access.as_str(),
        auth_source = %ctx.auth_source,
        request_id = %request_id,
        "memory scope"
    );

    req.extensions_mut().insert(ctx);
    next.run(req).await
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

    #[test]
    fn namespace_query_extract() {
        assert_eq!(namespace_from_query(None), "");
        assert_eq!(namespace_from_query(Some("foo=1")), "");
        assert_eq!(
            namespace_from_query(Some("namespace=agent-memory")),
            "agent-memory"
        );
        assert_eq!(namespace_from_query(Some("namespace=docs%2Dv2")), "docs-v2");
    }

    #[test]
    fn normalize_namespace_rules() {
        assert_eq!(normalize_namespace("").unwrap(), "");
        assert_eq!(normalize_namespace("agent-memory").unwrap(), "agent-memory");
        assert!(normalize_namespace("../x").is_err());
        assert!(normalize_namespace("Bad").is_err());
    }
}
