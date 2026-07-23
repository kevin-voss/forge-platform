use chrono::{DateTime, Utc};
use sqlx::{PgPool, Row};

/// Which secrets/config a service consumes in a project/env.
#[derive(Debug, Clone)]
pub struct BindingRow {
    pub project_id: String,
    pub environment: String,
    pub service: String,
    pub secret_names: Vec<String>,
    pub config_names: Vec<String>,
    pub updated_at: DateTime<Utc>,
}

/// Persist service bindings (names only — never values).
#[derive(Debug, Clone)]
pub struct BindingStore {
    pool: PgPool,
}

impl BindingStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    pub async fn upsert(
        &self,
        project_id: &str,
        environment: &str,
        service: &str,
        secret_names: &[String],
        config_names: &[String],
    ) -> Result<BindingRow, String> {
        let row = sqlx::query(
            r#"
            INSERT INTO service_bindings (
                project_id, environment, service, secret_names, config_names, updated_at
            )
            VALUES ($1, $2, $3, $4, $5, now())
            ON CONFLICT (project_id, environment, service)
            DO UPDATE SET
                secret_names = EXCLUDED.secret_names,
                config_names = EXCLUDED.config_names,
                updated_at = now()
            RETURNING project_id, environment, service, secret_names, config_names, updated_at
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(service)
        .bind(secret_names)
        .bind(config_names)
        .fetch_one(&self.pool)
        .await
        .map_err(|e| format!("upsert service_bindings: {e}"))?;

        Ok(row_from(row))
    }

    pub async fn get(
        &self,
        project_id: &str,
        environment: &str,
        service: &str,
    ) -> Result<Option<BindingRow>, String> {
        let row = sqlx::query(
            r#"
            SELECT project_id, environment, service, secret_names, config_names, updated_at
            FROM service_bindings
            WHERE project_id = $1 AND environment = $2 AND service = $3
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(service)
        .fetch_optional(&self.pool)
        .await
        .map_err(|e| format!("select service_bindings: {e}"))?;

        Ok(row.map(row_from))
    }
}

fn row_from(row: sqlx::postgres::PgRow) -> BindingRow {
    BindingRow {
        project_id: row.get("project_id"),
        environment: row.get("environment"),
        service: row.get("service"),
        secret_names: row.get("secret_names"),
        config_names: row.get("config_names"),
        updated_at: row.get("updated_at"),
    }
}
