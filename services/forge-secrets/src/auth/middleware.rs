//! SecretsAuth: bearer introspect + project isolation + Identity authz/check.

use crate::audit::hook::{denied_action_for_path, principal_label, record, source_from_headers};
use crate::audit::AuditResult;
use crate::auth::action_map::{map_action, AuthTarget};
use crate::auth::identity_client::{AuthzDecision, IdentityUnreachable, IntrospectResult};
use crate::state::AppState;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use tracing::{info, warn};

/// Authenticated principal attached to authorized requests.
#[derive(Debug, Clone)]
pub struct AuthPrincipal {
    pub principal_type: String,
    pub principal_id: String,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<&'static str>,
}

#[derive(Debug)]
pub enum AuthError {
    Unauthorized(&'static str),
    Forbidden(&'static str),
    Unavailable(&'static str),
}

impl AuthError {
    fn into_response(self) -> Response {
        match self {
            Self::Unauthorized(msg) => (
                StatusCode::UNAUTHORIZED,
                Json(ErrorBody {
                    error: msg.into(),
                    code: Some("unauthenticated"),
                }),
            )
                .into_response(),
            Self::Forbidden(msg) => (
                StatusCode::FORBIDDEN,
                Json(ErrorBody {
                    error: msg.into(),
                    code: Some("forbidden"),
                }),
            )
                .into_response(),
            Self::Unavailable(msg) => (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(ErrorBody {
                    error: msg.into(),
                    code: Some("identity_unavailable"),
                }),
            )
                .into_response(),
        }
    }
}

#[derive(Default)]
pub struct AuthMetrics {
    pub ok: AtomicU64,
    pub unauthorized: AtomicU64,
    pub forbidden: AtomicU64,
    pub unavailable: AtomicU64,
}

impl AuthMetrics {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }
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

/// Project isolation: scoped tokens must match path project; sessions must list membership.
pub fn isolation_allows(introspect: &IntrospectResult, path_project_id: &str) -> bool {
    if let Some(token_project) = introspect
        .project_id
        .as_deref()
        .map(str::trim)
        .filter(|s| !s.is_empty())
    {
        return token_project == path_project_id;
    }
    // Session tokens: require memberships include the path project when memberships are present.
    if let Some(memberships) = &introspect.memberships {
        if !memberships.projects.is_empty() {
            return memberships.projects.iter().any(|p| {
                p.project_id
                    .as_deref()
                    .map(|id| id == path_project_id)
                    .unwrap_or(false)
            });
        }
    }
    // No project_id and no memberships list — rely on Identity authz/check (membership).
    true
}

fn try_config_read_from_cache(
    identity: &Arc<dyn crate::auth::IdentityClient>,
    token: &str,
    path_project: &str,
) -> bool {
    let Some(cached_i) = identity.cached_introspect_result(token) else {
        return false;
    };
    if !cached_i.active || !isolation_allows(&cached_i, path_project) {
        return false;
    }
    let Some(ptype) = cached_i.principal_type.as_deref().filter(|s| !s.is_empty()) else {
        return false;
    };
    let Some(pid) = cached_i.principal_id.as_deref().filter(|s| !s.is_empty()) else {
        return false;
    };
    let Some(decision) = identity.cached_authz_decision(ptype, pid, path_project, "config.read")
    else {
        return false;
    };
    if decision.allow {
        info!(
            principal = %pid,
            project = %path_project,
            action = "config.read",
            allow = true,
            source = "cache",
            "authorization decision (identity down; cache hit)"
        );
        true
    } else {
        false
    }
}

pub async fn enforce(
    State(state): State<AppState>,
    mut req: Request<Body>,
    next: Next,
) -> Response {
    let method = req.method().as_str().to_string();
    let path = req.uri().path().to_string();
    let target = map_action(&method, &path);

    if matches!(target, AuthTarget::Skip) {
        return next.run(req).await;
    }

    if state.auth_mode.eq_ignore_ascii_case("dev") {
        warn!(
            path = %path,
            method = %method,
            "FORGE_AUTH_MODE=dev — secrets/config auth bypassed (insecure)"
        );
        req.extensions_mut().insert(AuthPrincipal {
            principal_type: "dev".into(),
            principal_id: "local".into(),
        });
        return next.run(req).await;
    }

    let AuthTarget::Authorize {
        action,
        project_id: path_project,
    } = target
    else {
        return next.run(req).await;
    };

    let source = source_from_headers(req.headers());
    let env_from_path = path_environment(&path);

    let auth_header = req
        .headers()
        .get(axum::http::header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());

    let Some(token) = parse_bearer(auth_header.as_deref()) else {
        state
            .auth_metrics
            .unauthorized
            .fetch_add(1, Ordering::Relaxed);
        return AuthError::Unauthorized("missing Authorization bearer token").into_response();
    };
    let token = token.to_string();

    let identity = match &state.identity {
        Some(client) => client.clone(),
        None => {
            state
                .auth_metrics
                .unavailable
                .fetch_add(1, Ordering::Relaxed);
            return AuthError::Unavailable("identity unavailable").into_response();
        }
    };

    let introspect = match identity.introspect(&token).await {
        Ok(v) => v,
        Err(IdentityUnreachable { message }) => {
            if action.as_str() == "config.read"
                && try_config_read_from_cache(&identity, &token, &path_project)
            {
                state.auth_metrics.ok.fetch_add(1, Ordering::Relaxed);
                if let Some(cached) = identity.cached_introspect_result(&token) {
                    if let (Some(pt), Some(pid)) =
                        (cached.principal_type.clone(), cached.principal_id.clone())
                    {
                        req.extensions_mut().insert(AuthPrincipal {
                            principal_type: pt,
                            principal_id: pid,
                        });
                    }
                }
                return next.run(req).await;
            }
            warn!(
                path = %path,
                method = %method,
                error = %message,
                "identity unreachable during introspect"
            );
            state
                .auth_metrics
                .unavailable
                .fetch_add(1, Ordering::Relaxed);
            return AuthError::Unavailable("identity unavailable").into_response();
        }
    };

    if !introspect.active {
        state
            .auth_metrics
            .unauthorized
            .fetch_add(1, Ordering::Relaxed);
        return AuthError::Unauthorized("inactive or unknown token").into_response();
    }

    let principal_type = introspect
        .principal_type
        .as_deref()
        .map(str::trim)
        .filter(|s| !s.is_empty());
    let principal_id = introspect
        .principal_id
        .as_deref()
        .map(str::trim)
        .filter(|s| !s.is_empty());
    let (Some(principal_type), Some(principal_id)) = (principal_type, principal_id) else {
        state
            .auth_metrics
            .unauthorized
            .fetch_add(1, Ordering::Relaxed);
        return AuthError::Unauthorized("token missing principal").into_response();
    };

    let auth_principal = AuthPrincipal {
        principal_type: principal_type.to_string(),
        principal_id: principal_id.to_string(),
    };
    let principal_str = principal_label(Some(&auth_principal), &state.auth_mode);

    if !isolation_allows(&introspect, &path_project) {
        state.auth_metrics.forbidden.fetch_add(1, Ordering::Relaxed);
        warn!(
            principal = %principal_id,
            token_project = introspect.project_id.as_deref().unwrap_or(""),
            path_project = %path_project,
            action = action.as_str(),
            "cross-tenant isolation denied"
        );
        info!(
            principal = %principal_id,
            project = %path_project,
            action = action.as_str(),
            allow = false,
            "authorization decision"
        );
        if let Some(audit_action) = denied_action_for_path(&method, &path) {
            record(
                &state,
                &path_project,
                env_from_path.as_deref(),
                audit_action,
                &principal_str,
                path_resource_name(&path),
                None,
                AuditResult::Denied,
                source.as_deref(),
            )
            .await;
        }
        return AuthError::Forbidden("project isolation denied").into_response();
    }

    let decision = match identity
        .check_authz(principal_type, principal_id, &path_project, action.as_str())
        .await
    {
        Ok(d) => d,
        Err(IdentityUnreachable { message }) => {
            if action.as_str() == "config.read" {
                if let Some(cached) = identity.cached_authz_decision(
                    principal_type,
                    principal_id,
                    &path_project,
                    action.as_str(),
                ) {
                    if cached.allow {
                        info!(
                            principal = %principal_id,
                            project = %path_project,
                            action = action.as_str(),
                            allow = true,
                            source = "cache",
                            "authorization decision (identity down; cache hit)"
                        );
                        state.auth_metrics.ok.fetch_add(1, Ordering::Relaxed);
                        req.extensions_mut().insert(auth_principal);
                        return next.run(req).await;
                    }
                }
            }
            warn!(
                principal = %principal_id,
                action = action.as_str(),
                project = %path_project,
                error = %message,
                "identity unreachable during authz"
            );
            state
                .auth_metrics
                .unavailable
                .fetch_add(1, Ordering::Relaxed);
            return AuthError::Unavailable("identity unavailable").into_response();
        }
    };

    log_decision(principal_id, &path_project, action.as_str(), &decision);

    if !decision.allow {
        state.auth_metrics.forbidden.fetch_add(1, Ordering::Relaxed);
        if let Some(audit_action) = denied_action_for_path(&method, &path) {
            record(
                &state,
                &path_project,
                env_from_path.as_deref(),
                audit_action,
                &principal_str,
                path_resource_name(&path),
                None,
                AuditResult::Denied,
                source.as_deref(),
            )
            .await;
        }
        return AuthError::Forbidden("forbidden").into_response();
    }

    state.auth_metrics.ok.fetch_add(1, Ordering::Relaxed);
    req.extensions_mut().insert(auth_principal);
    next.run(req).await
}

fn path_environment(path: &str) -> Option<String> {
    let rest = path.strip_prefix("/v1/projects/")?;
    let mut parts = rest.split('/');
    let _pid = parts.next()?;
    if parts.next()? != "envs" {
        return None;
    }
    parts.next().map(str::to_string).filter(|s| !s.is_empty())
}

fn path_resource_name(path: &str) -> Option<&str> {
    let trimmed = path.trim_end_matches('/');
    let name = trimmed.rsplit('/').next()?;
    let name = name.strip_suffix(":access").unwrap_or(name);
    if matches!(
        name,
        "secrets" | "config" | "bindings" | "resolve" | "audit" | "services" | "envs"
    ) {
        return None;
    }
    Some(name)
}

fn log_decision(principal: &str, project: &str, action: &str, decision: &AuthzDecision) {
    info!(
        principal = %principal,
        project = %project,
        action = %action,
        allow = decision.allow,
        role = %decision.role,
        reason = %decision.reason,
        "authorization decision"
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::identity_client::{IntrospectMembershipProject, IntrospectMemberships};

    #[test]
    fn parse_bearer_ok() {
        assert_eq!(parse_bearer(Some("Bearer abc")), Some("abc"));
        assert_eq!(parse_bearer(Some("bearer xyz")), Some("xyz"));
        assert!(parse_bearer(Some("Basic x")).is_none());
        assert!(parse_bearer(None).is_none());
    }

    #[test]
    fn isolation_rejects_mismatched_token_project() {
        let intro = IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("u1".into()),
            user_id: None,
            project_id: Some("prj_a".into()),
            role: Some("developer".into()),
            memberships: None,
        };
        assert!(!isolation_allows(&intro, "prj_b"));
        assert!(isolation_allows(&intro, "prj_a"));
    }

    #[test]
    fn isolation_uses_memberships_when_no_token_project() {
        let intro = IntrospectResult {
            active: true,
            principal_type: Some("user".into()),
            principal_id: Some("u1".into()),
            user_id: Some("u1".into()),
            project_id: None,
            role: None,
            memberships: Some(IntrospectMemberships {
                projects: vec![IntrospectMembershipProject {
                    project_id: Some("prj_1".into()),
                    role: Some("viewer".into()),
                }],
            }),
        };
        assert!(isolation_allows(&intro, "prj_1"));
        assert!(!isolation_allows(&intro, "prj_2"));
    }
}
