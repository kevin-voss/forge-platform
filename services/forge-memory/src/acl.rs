//! Read/write ACL hook over Identity roles (audited).

use crate::scope::{AccessKind, ProjectContext};
use crate::state::AppState;
use std::sync::atomic::Ordering;
use tracing::{info, warn};

/// Roles that may only read (deny write).
fn is_read_only_role(role: &str) -> bool {
    matches!(
        role.trim().to_ascii_lowercase().as_str(),
        "viewer" | "reader" | "read-only" | "readonly" | "read_only"
    )
}

/// Whether the scoped principal may perform `kind` on the project.
pub fn check_access(ctx: &ProjectContext, kind: AccessKind) -> Result<(), AclDeny> {
    match kind {
        AccessKind::Read => Ok(()),
        AccessKind::Write => match ctx.role.as_deref() {
            Some(role) if is_read_only_role(role) => Err(AclDeny {
                reason: "role_read_only",
                message: format!("role {role:?} cannot write memory"),
            }),
            _ => Ok(()),
        },
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AclDeny {
    pub reason: &'static str,
    pub message: String,
}

/// Record a denial (metric + audit log) and return the deny reason code.
pub fn deny(state: &AppState, ctx: &ProjectContext, kind: AccessKind, deny: &AclDeny) {
    state
        .metrics
        .memory_acl_denied_total
        .fetch_add(1, Ordering::Relaxed);
    warn!(
        project_id = %ctx.project_id,
        namespace = %ctx.namespace,
        role = ctx.role.as_deref().unwrap_or("-"),
        access = %kind.as_str(),
        reason = deny.reason,
        "memory ACL denied"
    );
}

/// Audit an allowed access (info-level for write; debug-equivalent info for read is skipped).
pub fn audit_allow(ctx: &ProjectContext, kind: AccessKind) {
    if matches!(kind, AccessKind::Write) {
        info!(
            project_id = %ctx.project_id,
            namespace = %ctx.namespace,
            role = ctx.role.as_deref().unwrap_or("-"),
            access = "write",
            "memory ACL allow"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx(role: Option<&str>) -> ProjectContext {
        ProjectContext {
            project_id: "proj-a".into(),
            namespace: String::new(),
            role: role.map(str::to_string),
            auth_source: "test".into(),
        }
    }

    #[test]
    fn viewer_cannot_write() {
        let err = check_access(&ctx(Some("viewer")), AccessKind::Write).unwrap_err();
        assert_eq!(err.reason, "role_read_only");
        assert!(check_access(&ctx(Some("viewer")), AccessKind::Read).is_ok());
    }

    #[test]
    fn developer_can_write() {
        assert!(check_access(&ctx(Some("developer")), AccessKind::Write).is_ok());
        assert!(check_access(&ctx(None), AccessKind::Write).is_ok());
    }
}
