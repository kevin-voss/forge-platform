use chrono::{DateTime, Utc};
use sqlx::{PgPool, Row};

/// One plaintext config value (non-secret by definition).
#[derive(Debug, Clone)]
pub struct ConfigRow {
    pub project_id: String,
    pub environment: String,
    pub name: String,
    pub value: String,
    pub updated_at: DateTime<Utc>,
}

/// Persist project/env-scoped configuration (plaintext, listable with values).
#[derive(Debug, Clone)]
pub struct ConfigStore {
    pool: PgPool,
}

impl ConfigStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    pub async fn upsert(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
        value: &str,
    ) -> Result<ConfigRow, String> {
        let row = sqlx::query(
            r#"
            INSERT INTO config_values (project_id, environment, name, value, updated_at)
            VALUES ($1, $2, $3, $4, now())
            ON CONFLICT (project_id, environment, name)
            DO UPDATE SET value = EXCLUDED.value, updated_at = now()
            RETURNING project_id, environment, name, value, updated_at
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .bind(value)
        .fetch_one(&self.pool)
        .await
        .map_err(|e| format!("upsert config_values: {e}"))?;

        Ok(ConfigRow {
            project_id: row.get("project_id"),
            environment: row.get("environment"),
            name: row.get("name"),
            value: row.get("value"),
            updated_at: row.get("updated_at"),
        })
    }

    pub async fn get(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
    ) -> Result<Option<ConfigRow>, String> {
        let row = sqlx::query(
            r#"
            SELECT project_id, environment, name, value, updated_at
            FROM config_values
            WHERE project_id = $1 AND environment = $2 AND name = $3
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .fetch_optional(&self.pool)
        .await
        .map_err(|e| format!("get config_values: {e}"))?;

        Ok(row.map(|r| ConfigRow {
            project_id: r.get("project_id"),
            environment: r.get("environment"),
            name: r.get("name"),
            value: r.get("value"),
            updated_at: r.get("updated_at"),
        }))
    }

    pub async fn list(
        &self,
        project_id: &str,
        environment: &str,
    ) -> Result<Vec<ConfigRow>, String> {
        let rows = sqlx::query(
            r#"
            SELECT project_id, environment, name, value, updated_at
            FROM config_values
            WHERE project_id = $1 AND environment = $2
            ORDER BY name ASC
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .fetch_all(&self.pool)
        .await
        .map_err(|e| format!("list config_values: {e}"))?;

        Ok(rows
            .into_iter()
            .map(|r| ConfigRow {
                project_id: r.get("project_id"),
                environment: r.get("environment"),
                name: r.get("name"),
                value: r.get("value"),
                updated_at: r.get("updated_at"),
            })
            .collect())
    }

    /// Returns true if a row was deleted.
    pub async fn delete(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
    ) -> Result<bool, String> {
        let result = sqlx::query(
            r#"
            DELETE FROM config_values
            WHERE project_id = $1 AND environment = $2 AND name = $3
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .execute(&self.pool)
        .await
        .map_err(|e| format!("delete config_values: {e}"))?;
        Ok(result.rows_affected() > 0)
    }
}
