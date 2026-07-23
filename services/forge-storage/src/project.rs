//! Project-scope middleware: Identity bearer, `X-Forge-Project` (dev), or signed object token.

use crate::config::AuthMode;
use crate::signing::{looks_like_signed_token, verify_token, VerifyError};
use crate::state::AppState;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use std::sync::atomic::Ordering;
use tracing::warn;

pub const HEADER_PROJECT: &str = "x-forge-project";

/// Project context attached to authorized storage requests.
#[derive(Debug, Clone)]
pub struct ProjectContext {
    pub project_id: String,
}

/// Set when auth succeeded via a signed object token (13.05).
#[derive(Debug, Clone)]
pub struct SignedTokenAuth {
    pub method: String,
    pub bucket: String,
    pub key: String,
    pub exp: i64,
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

fn query_token(req: &Request<Body>) -> Option<String> {
    let query = req.uri().query()?;
    for pair in query.split('&') {
        let mut kv = pair.splitn(2, '=');
        let k = kv.next()?;
        if k != "token" {
            continue;
        }
        let v = kv.next().unwrap_or("");
        if v.is_empty() {
            return None;
        }
        // Percent-decode minimally (+ → space is not used for base64url).
        let decoded = percent_decode(v);
        if decoded.is_empty() {
            return None;
        }
        return Some(decoded);
    }
    None
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

/// Parse `/v1/buckets/{bucket}/objects/{key...}` (key may include `/`).
fn parse_object_path(path: &str) -> Option<(String, String)> {
    let rest = path.strip_prefix("/v1/buckets/")?;
    let (bucket, after) = rest.split_once("/objects/")?;
    if bucket.is_empty() || after.is_empty() {
        return None;
    }
    let key = after.trim_start_matches('/');
    if key.is_empty() {
        return None;
    }
    Some((bucket.to_string(), key.to_string()))
}

fn record_rejection(state: &AppState, reason: &str) {
    state
        .metrics
        .storage_token_rejections_total
        .fetch_add(1, Ordering::Relaxed);
    warn!(reason = %reason, "signed token rejected");
}

fn verify_error_response(state: &AppState, err: VerifyError) -> Response {
    record_rejection(state, err.reason());
    match err {
        VerifyError::NotOurFormat | VerifyError::InvalidToken => {
            unauthorized("invalid token", "invalid_token")
        }
        VerifyError::TokenExpired => unauthorized("token expired", "token_expired"),
        VerifyError::MethodMismatch => {
            forbidden("token method does not match request", "method_mismatch")
        }
        VerifyError::ScopeMismatch => {
            forbidden("token scope does not match object", "scope_mismatch")
        }
    }
}

/// Attempt signed-token auth for object GET/PUT/HEAD.
///
/// Returns `Some(Response)` when a token was presented and auth should stop
/// (success continues via `None` after mutating `req`, or error response).
async fn try_signed_token_auth(
    state: &AppState,
    req: &mut Request<Body>,
) -> Option<Result<(), Response>> {
    let from_query = query_token(req);
    let auth_header = req
        .headers()
        .get(axum::http::header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());
    let from_bearer = parse_bearer(auth_header.as_deref()).map(str::to_string);

    let (token, required) = if let Some(t) = from_query {
        (t, true)
    } else if let Some(t) = from_bearer {
        if looks_like_signed_token(&t) {
            (t, true)
        } else {
            return None; // Identity / other bearer — fall through
        }
    } else {
        return None;
    };

    let method = req.method().as_str();
    // HEAD is treated as GET for token scope (read-only metadata of same object).
    let verify_method = if method.eq_ignore_ascii_case("HEAD") {
        "GET"
    } else if method.eq_ignore_ascii_case("GET") || method.eq_ignore_ascii_case("PUT") {
        method
    } else {
        if required {
            return Some(Err(verify_error_response(
                state,
                VerifyError::MethodMismatch,
            )));
        }
        return None;
    };

    let Some((bucket, key)) = parse_object_path(req.uri().path()) else {
        if required {
            return Some(Err(unauthorized("invalid token", "invalid_token")));
        }
        return None;
    };

    let Some(signing) = state.signing.as_ref() else {
        return Some(Err(unauthorized("invalid token", "invalid_token")));
    };

    let now = (state.clock)();
    match verify_token(signing, &token, verify_method, None, &bucket, &key, now) {
        Ok(claims) => {
            req.extensions_mut().insert(ProjectContext {
                project_id: claims.project_id.clone(),
            });
            req.extensions_mut().insert(SignedTokenAuth {
                method: claims.method,
                bucket: claims.bucket,
                key: claims.key,
                exp: claims.exp,
            });
            Some(Ok(()))
        }
        Err(err) => Some(Err(verify_error_response(state, err))),
    }
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

    match try_signed_token_auth(&state, &mut req).await {
        Some(Ok(())) => return next.run(req).await,
        Some(Err(resp)) => return resp,
        None => {}
    }

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
                    return unauthorized("project not permitted for token", "unauthenticated");
                }
            } else {
                return unauthorized(
                    "token has no project_id; provide X-Forge-Project",
                    "unauthenticated",
                );
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

    #[test]
    fn parse_object_path_nested() {
        let (b, k) = parse_object_path("/v1/buckets/artifacts/objects/dir/a.bin").unwrap();
        assert_eq!(b, "artifacts");
        assert_eq!(k, "dir/a.bin");
    }

    #[test]
    fn percent_decode_token() {
        assert_eq!(percent_decode("ab%2Fcd"), "ab/cd");
    }
}
