//! Shared helpers for recording audit events from HTTP handlers / middleware.

use crate::audit::recorder::{AuditEvent, AuditRecorder, AuditResult};
use crate::auth::middleware::AuthPrincipal;
use crate::state::AppState;
use axum::http::HeaderMap;

pub fn principal_label(principal: Option<&AuthPrincipal>, auth_mode: &str) -> String {
    match principal {
        Some(p) => format!("{}:{}", p.principal_type, p.principal_id),
        None if auth_mode.eq_ignore_ascii_case("dev") => "dev".into(),
        None => "unknown".into(),
    }
}

pub fn source_from_headers(headers: &HeaderMap) -> Option<String> {
    headers
        .get("x-forwarded-for")
        .and_then(|v| v.to_str().ok())
        .map(|s| s.split(',').next().unwrap_or(s).trim().to_string())
        .filter(|s| !s.is_empty())
        .or_else(|| {
            headers
                .get("x-forge-service")
                .and_then(|v| v.to_str().ok())
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
        })
}

pub fn recorder(state: &AppState) -> AuditRecorder {
    AuditRecorder::new(
        state.pool.clone(),
        state.audit_enabled,
        state.audit_strict,
        state.audit_metrics.clone(),
    )
}

/// Persist an audit event (metadata only — never a secret value).
#[allow(clippy::too_many_arguments)]
pub async fn record(
    state: &AppState,
    project_id: &str,
    environment: Option<&str>,
    action: &str,
    principal: &str,
    name: Option<&str>,
    version: Option<i32>,
    result: AuditResult,
    source: Option<&str>,
) {
    let event = AuditEvent {
        project_id: project_id.to_string(),
        environment: environment.map(str::to_string),
        action: action.to_string(),
        principal: principal.to_string(),
        name: name.map(str::to_string),
        version,
        result,
        source: source.map(str::to_string),
    };
    let _ = recorder(state).record(event).await;
}

/// Map an authorized HTTP target to an audit action name for denied attempts.
pub fn denied_action_for_path(method: &str, path: &str) -> Option<&'static str> {
    let m = method.to_ascii_uppercase();
    let p = path.trim_end_matches('/');
    if p.contains("/audit") && m == "GET" {
        return Some("audit.query");
    }
    if p.contains("/resolve") && m == "POST" {
        return Some("resolve");
    }
    if p.contains("/secrets/") {
        if m == "PUT" {
            return Some("secret.set");
        }
        if m == "DELETE" {
            return Some("secret.delete");
        }
        if m == "POST" && p.contains(":access") {
            return Some("secret.access");
        }
        return Some("secret.access");
    }
    if p.ends_with("/secrets") && m == "GET" {
        return Some("secret.access");
    }
    if p.contains("/config") {
        return match m.as_str() {
            "PUT" => Some("config.set"),
            "DELETE" => Some("config.set"),
            _ => Some("config.set"),
        };
    }
    if p.contains("/bindings") {
        return Some("secret.set");
    }
    None
}
