use chrono::{DateTime, Utc};
use sqlx::{PgPool, Row};

/// One encrypted secret version as stored (never includes plaintext).
#[derive(Debug, Clone)]
pub struct SecretRow {
    pub project_id: String,
    pub environment: String,
    pub name: String,
    pub version: i32,
    pub ciphertext: Vec<u8>,
    pub nonce: Vec<u8>,
    pub data_key_version: i32,
    pub created_at: DateTime<Utc>,
}

/// Metadata for a single version (no ciphertext / plaintext).
#[derive(Debug, Clone)]
pub struct SecretVersionMeta {
    pub version: i32,
    pub created_at: DateTime<Utc>,
    pub data_key_version: i32,
}

/// Aggregated list-row metadata for a secret name.
#[derive(Debug, Clone)]
pub struct SecretListItem {
    pub name: String,
    pub version: i32,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Inputs for inserting one encrypted secret version.
#[derive(Debug, Clone)]
pub struct NewSecretVersion<'a> {
    pub project_id: &'a str,
    pub environment: &'a str,
    pub name: &'a str,
    pub version: i32,
    pub ciphertext: &'a [u8],
    pub nonce: &'a [u8],
    pub data_key_version: i32,
}

/// Persist encrypted secret versions and query metadata only.
#[derive(Debug, Clone)]
pub struct SecretStore {
    pool: PgPool,
}

impl SecretStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    /// Next monotonic version for (project, env, name). Starts at 1.
    pub async fn next_version(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
    ) -> Result<i32, String> {
        let row = sqlx::query(
            r#"
            SELECT COALESCE(MAX(version), 0)::int AS max_version
            FROM secrets
            WHERE project_id = $1 AND environment = $2 AND name = $3
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .fetch_one(&self.pool)
        .await
        .map_err(|e| format!("select max secret version: {e}"))?;
        Ok(row.get::<i32, _>("max_version") + 1)
    }

    pub async fn insert_version(&self, input: &NewSecretVersion<'_>) -> Result<SecretRow, String> {
        let row = sqlx::query(
            r#"
            INSERT INTO secrets (
                project_id, environment, name, version,
                ciphertext, nonce, data_key_version
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            RETURNING project_id, environment, name, version,
                      ciphertext, nonce, data_key_version, created_at
            "#,
        )
        .bind(input.project_id)
        .bind(input.environment)
        .bind(input.name)
        .bind(input.version)
        .bind(input.ciphertext)
        .bind(input.nonce)
        .bind(input.data_key_version)
        .fetch_one(&self.pool)
        .await
        .map_err(|e| format!("insert secret version: {e}"))?;

        Ok(SecretRow {
            project_id: row.get("project_id"),
            environment: row.get("environment"),
            name: row.get("name"),
            version: row.get("version"),
            ciphertext: row.get("ciphertext"),
            nonce: row.get("nonce"),
            data_key_version: row.get("data_key_version"),
            created_at: row.get("created_at"),
        })
    }

    /// List current metadata per secret name (latest version). Never returns values.
    pub async fn list_metadata(
        &self,
        project_id: &str,
        environment: &str,
    ) -> Result<Vec<SecretListItem>, String> {
        let rows = sqlx::query(
            r#"
            SELECT
                s.name,
                s.version,
                s.created_at AS updated_at,
                first_v.created_at AS created_at
            FROM secrets s
            INNER JOIN (
                SELECT name, MAX(version) AS max_version
                FROM secrets
                WHERE project_id = $1 AND environment = $2
                GROUP BY name
            ) latest
              ON latest.name = s.name AND latest.max_version = s.version
            INNER JOIN (
                SELECT name, MIN(created_at) AS created_at
                FROM secrets
                WHERE project_id = $1 AND environment = $2
                GROUP BY name
            ) first_v ON first_v.name = s.name
            WHERE s.project_id = $1 AND s.environment = $2
            ORDER BY s.name
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .fetch_all(&self.pool)
        .await
        .map_err(|e| format!("list secret metadata: {e}"))?;

        Ok(rows
            .into_iter()
            .map(|r| SecretListItem {
                name: r.get("name"),
                version: r.get("version"),
                created_at: r.get("created_at"),
                updated_at: r.get("updated_at"),
            })
            .collect())
    }

    /// Version history for one secret (metadata only).
    pub async fn version_history(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
    ) -> Result<Vec<SecretVersionMeta>, String> {
        let rows = sqlx::query(
            r#"
            SELECT version, created_at, data_key_version
            FROM secrets
            WHERE project_id = $1 AND environment = $2 AND name = $3
            ORDER BY version ASC
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .fetch_all(&self.pool)
        .await
        .map_err(|e| format!("select secret history: {e}"))?;

        Ok(rows
            .into_iter()
            .map(|r| SecretVersionMeta {
                version: r.get("version"),
                created_at: r.get("created_at"),
                data_key_version: r.get("data_key_version"),
            })
            .collect())
    }

    /// Fetch a specific version (or latest when `version` is None) for decrypt.
    pub async fn fetch_for_decrypt(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
        version: Option<i32>,
    ) -> Result<Option<SecretRow>, String> {
        let row = if let Some(v) = version {
            sqlx::query(
                r#"
                SELECT project_id, environment, name, version,
                       ciphertext, nonce, data_key_version, created_at
                FROM secrets
                WHERE project_id = $1 AND environment = $2 AND name = $3 AND version = $4
                "#,
            )
            .bind(project_id)
            .bind(environment)
            .bind(name)
            .bind(v)
            .fetch_optional(&self.pool)
            .await
        } else {
            sqlx::query(
                r#"
                SELECT project_id, environment, name, version,
                       ciphertext, nonce, data_key_version, created_at
                FROM secrets
                WHERE project_id = $1 AND environment = $2 AND name = $3
                ORDER BY version DESC
                LIMIT 1
                "#,
            )
            .bind(project_id)
            .bind(environment)
            .bind(name)
            .fetch_optional(&self.pool)
            .await
        }
        .map_err(|e| format!("select secret for decrypt: {e}"))?;

        Ok(row.map(|r| SecretRow {
            project_id: r.get("project_id"),
            environment: r.get("environment"),
            name: r.get("name"),
            version: r.get("version"),
            ciphertext: r.get("ciphertext"),
            nonce: r.get("nonce"),
            data_key_version: r.get("data_key_version"),
            created_at: r.get("created_at"),
        }))
    }

    /// Count rows that contain the exact plaintext bytes inside ciphertext (should be 0).
    pub async fn ciphertext_contains_plaintext(
        &self,
        project_id: &str,
        environment: &str,
        name: &str,
        plaintext: &[u8],
    ) -> Result<bool, String> {
        let row = sqlx::query(
            r#"
            SELECT EXISTS(
                SELECT 1 FROM secrets
                WHERE project_id = $1 AND environment = $2 AND name = $3
                  AND position($4::bytea in ciphertext) > 0
            ) AS hit
            "#,
        )
        .bind(project_id)
        .bind(environment)
        .bind(name)
        .bind(plaintext)
        .fetch_one(&self.pool)
        .await
        .map_err(|e| format!("check plaintext in ciphertext: {e}"))?;
        Ok(row.get::<bool, _>("hit"))
    }
}
