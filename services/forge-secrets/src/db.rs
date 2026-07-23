use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Row};
use std::path::Path;
use std::time::Duration;
use tracing::{info, warn};

pub async fn connect(db_url: &str) -> Result<PgPool, String> {
    PgPoolOptions::new()
        .max_connections(10)
        .acquire_timeout(Duration::from_secs(5))
        .connect(db_url)
        .await
        .map_err(|e| format!("connect postgres: {e}"))
}

pub async fn migrate(pool: &PgPool) -> Result<(), String> {
    let candidates = ["./migrations", "/app/migrations"];
    let mut last_err = None;
    for dir in candidates {
        let path = Path::new(dir);
        if !path.is_dir() {
            continue;
        }
        match sqlx::migrate::Migrator::new(path).await {
            Ok(migrator) => {
                migrator
                    .run(pool)
                    .await
                    .map_err(|e| format!("migrate ({dir}): {e}"))?;
                info!(migrations_dir = dir, "database migrations applied");
                return Ok(());
            }
            Err(e) => {
                last_err = Some(format!("load migrator ({dir}): {e}"));
            }
        }
    }
    Err(last_err.unwrap_or_else(|| {
        "no migrations directory found (./migrations or /app/migrations)".into()
    }))
}

pub async fn ping(pool: &PgPool) -> Result<(), String> {
    sqlx::query("SELECT 1")
        .execute(pool)
        .await
        .map_err(|e| format!("db ping: {e}"))?;
    Ok(())
}

/// Connect with bounded retries (used at startup; readiness keeps retrying via ping).
pub async fn connect_with_retry(
    db_url: &str,
    attempts: u32,
    delay: Duration,
) -> Result<PgPool, String> {
    let mut last = String::from("connect failed");
    for i in 1..=attempts.max(1) {
        match connect(db_url).await {
            Ok(pool) => return Ok(pool),
            Err(err) => {
                last = err;
                warn!(attempt = i, attempts, error = %last, "postgres connect failed; retrying");
                if i < attempts {
                    tokio::time::sleep(delay).await;
                }
            }
        }
    }
    Err(last)
}

#[derive(Debug, Clone)]
pub struct ProjectDataKeyRow {
    pub project_id: String,
    pub wrapped_key: Vec<u8>,
    pub key_version: i32,
    pub master_key_id: String,
}

pub async fn insert_project_data_key(
    pool: &PgPool,
    project_id: &str,
    wrapped_key: &[u8],
    key_version: i32,
    master_key_id: &str,
) -> Result<(), String> {
    sqlx::query(
        r#"
        INSERT INTO project_data_keys (project_id, wrapped_key, key_version, master_key_id)
        VALUES ($1, $2, $3, $4)
        "#,
    )
    .bind(project_id)
    .bind(wrapped_key)
    .bind(key_version)
    .bind(master_key_id)
    .execute(pool)
    .await
    .map_err(|e| format!("insert project_data_keys: {e}"))?;
    Ok(())
}

pub async fn get_project_data_key(
    pool: &PgPool,
    project_id: &str,
) -> Result<Option<ProjectDataKeyRow>, String> {
    let row = sqlx::query(
        r#"
        SELECT project_id, wrapped_key, key_version, master_key_id
        FROM project_data_keys
        WHERE project_id = $1
        "#,
    )
    .bind(project_id)
    .fetch_optional(pool)
    .await
    .map_err(|e| format!("select project_data_keys: {e}"))?;

    Ok(row.map(|r| ProjectDataKeyRow {
        project_id: r.get("project_id"),
        wrapped_key: r.get("wrapped_key"),
        key_version: r.get("key_version"),
        master_key_id: r.get("master_key_id"),
    }))
}

pub async fn count_project_data_keys(pool: &PgPool) -> Result<i64, String> {
    let row = sqlx::query("SELECT COUNT(*)::bigint AS c FROM project_data_keys")
        .fetch_one(pool)
        .await
        .map_err(|e| format!("count project_data_keys: {e}"))?;
    Ok(row.get::<i64, _>("c"))
}

pub async fn count_secrets(pool: &PgPool) -> Result<i64, String> {
    let row = sqlx::query("SELECT COUNT(*)::bigint AS c FROM secrets")
        .fetch_one(pool)
        .await
        .map_err(|e| format!("count secrets: {e}"))?;
    Ok(row.get::<i64, _>("c"))
}

pub async fn count_config_values(pool: &PgPool) -> Result<i64, String> {
    let row = sqlx::query("SELECT COUNT(*)::bigint AS c FROM config_values")
        .fetch_one(pool)
        .await
        .map_err(|e| format!("count config_values: {e}"))?;
    Ok(row.get::<i64, _>("c"))
}
