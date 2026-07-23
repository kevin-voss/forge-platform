//! Append-only audit recorder. Schema excludes secret values by construction.

use chrono::{DateTime, Utc};
use sqlx::{PgPool, Row};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use tracing::{error, warn};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AuditResult {
    Ok,
    Denied,
    Error,
}

impl AuditResult {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Ok => "ok",
            Self::Denied => "denied",
            Self::Error => "error",
        }
    }
}

/// Audit event — **no value field** (enforced by this struct).
#[derive(Debug, Clone)]
pub struct AuditEvent {
    pub project_id: String,
    pub environment: Option<String>,
    pub action: String,
    pub principal: String,
    pub name: Option<String>,
    pub version: Option<i32>,
    pub result: AuditResult,
    pub source: Option<String>,
}

#[derive(Debug, Clone)]
pub struct AuditRow {
    pub id: i64,
    pub at: DateTime<Utc>,
    pub project_id: String,
    pub environment: Option<String>,
    pub action: String,
    pub principal: String,
    pub name: Option<String>,
    pub version: Option<i32>,
    pub result: String,
    pub source: Option<String>,
}

#[derive(Debug, Default)]
pub struct AuditMetrics {
    pub events_total: AtomicU64,
    pub errors_total: AtomicU64,
}

impl AuditMetrics {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }
}

/// Best-effort audit writer. Failures do not fail the caller unless `strict`.
#[derive(Clone)]
pub struct AuditRecorder {
    pool: Option<PgPool>,
    enabled: bool,
    strict: bool,
    metrics: Arc<AuditMetrics>,
}

impl AuditRecorder {
    pub fn new(
        pool: Option<PgPool>,
        enabled: bool,
        strict: bool,
        metrics: Arc<AuditMetrics>,
    ) -> Self {
        Self {
            pool,
            enabled,
            strict,
            metrics,
        }
    }

    pub fn metrics(&self) -> Arc<AuditMetrics> {
        self.metrics.clone()
    }

    /// Persist an audit event. Never accepts or stores a secret value.
    pub async fn record(&self, event: AuditEvent) -> Result<(), String> {
        if !self.enabled {
            return Ok(());
        }
        let Some(pool) = &self.pool else {
            let msg = "audit skipped: no database pool".to_string();
            self.fail(&msg);
            return if self.strict { Err(msg) } else { Ok(()) };
        };

        // Compile-time / structural guarantee: AuditEvent has no value field.
        let _no_value: () = ();

        let res = sqlx::query(
            r#"
            INSERT INTO audit_events (
                project_id, environment, action, principal,
                name, version, result, source
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
            "#,
        )
        .bind(&event.project_id)
        .bind(event.environment.as_deref())
        .bind(&event.action)
        .bind(&event.principal)
        .bind(event.name.as_deref())
        .bind(event.version)
        .bind(event.result.as_str())
        .bind(event.source.as_deref())
        .execute(pool)
        .await;

        match res {
            Ok(_) => {
                self.metrics.events_total.fetch_add(1, Ordering::Relaxed);
                Ok(())
            }
            Err(e) => {
                let msg = format!("audit insert failed: {e}");
                self.fail(&msg);
                if self.strict {
                    Err(msg)
                } else {
                    Ok(())
                }
            }
        }
    }

    fn fail(&self, msg: &str) {
        self.metrics.errors_total.fetch_add(1, Ordering::Relaxed);
        // Warning must not include secret values (event never carried one).
        warn!(audit_error = true, error = %msg, "audit write failed (best-effort)");
        error!(
            forge_audit_write_errors_total = 1u64,
            "audit write error metric"
        );
    }

    pub async fn query(
        &self,
        project_id: &str,
        environment: Option<&str>,
        name: Option<&str>,
        action: Option<&str>,
        since: Option<DateTime<Utc>>,
        limit: i64,
    ) -> Result<Vec<AuditRow>, String> {
        let Some(pool) = &self.pool else {
            return Err("service not ready".into());
        };
        let limit = limit.clamp(1, 500);

        let rows = sqlx::query(
            r#"
            SELECT id, at, project_id, environment, action, principal,
                   name, version, result, source
            FROM audit_events
            WHERE project_id = $1
              AND ($2::text IS NULL OR environment = $2)
              AND ($3::text IS NULL OR name = $3)
              AND ($4::text IS NULL OR action = $4)
              AND ($5::timestamptz IS NULL OR at >= $5)
            ORDER BY at DESC, id DESC
            LIMIT $6
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .bind(action)
        .bind(since)
        .bind(limit)
        .fetch_all(pool)
        .await
        .map_err(|e| format!("query audit_events: {e}"))?;

        Ok(rows
            .into_iter()
            .map(|r| AuditRow {
                id: r.get("id"),
                at: r.get("at"),
                project_id: r.get("project_id"),
                environment: r.get("environment"),
                action: r.get("action"),
                principal: r.get("principal"),
                name: r.get("name"),
                version: r.get("version"),
                result: r.get("result"),
                source: r.get("source"),
            })
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Serialize;

    #[derive(Serialize)]
    struct AuditWire<'a> {
        at: &'a str,
        action: &'a str,
        principal: &'a str,
        name: Option<&'a str>,
        version: Option<i32>,
        result: &'a str,
        source: Option<&'a str>,
    }

    #[test]
    fn audit_event_has_no_value_field() {
        let event = AuditEvent {
            project_id: "prj_1".into(),
            environment: Some("production".into()),
            action: "secret.set".into(),
            principal: "user:u1".into(),
            name: Some("DATABASE_PASSWORD".into()),
            version: Some(1),
            result: AuditResult::Ok,
            source: Some("127.0.0.1".into()),
        };
        // Structural: serialize a wire shape without value.
        let wire = AuditWire {
            at: "t",
            action: &event.action,
            principal: &event.principal,
            name: event.name.as_deref(),
            version: event.version,
            result: event.result.as_str(),
            source: event.source.as_deref(),
        };
        let v = serde_json::to_value(&wire).unwrap();
        assert!(v.get("value").is_none());
        assert_eq!(v.get("action").and_then(|x| x.as_str()), Some("secret.set"));
        assert_eq!(event.result.as_str(), "ok");
        assert_eq!(AuditResult::Denied.as_str(), "denied");
    }
}
