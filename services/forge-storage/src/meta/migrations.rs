//! SQLite schema migrations for the metadata index.

/// Embedded initial schema (source: `migrations/0001_init.sql`).
pub const MIGRATION_0001: &str = include_str!("../../migrations/0001_init.sql");

/// Add `storage_path` for streamed object payloads (source: `migrations/0002_storage_path.sql`).
pub const MIGRATION_0002: &str = include_str!("../../migrations/0002_storage_path.sql");

/// Content-addressed blob refcounts (source: `migrations/0003_sha_refcount.sql`).
pub const MIGRATION_0003: &str = include_str!("../../migrations/0003_sha_refcount.sql");

/// Per-project quotas + usage counters (source: `migrations/0004_quota_blobs.sql`).
pub const MIGRATION_0004: &str = include_str!("../../migrations/0004_quota_blobs.sql");

/// Apply all migrations to an open connection.
pub fn apply(conn: &rusqlite::Connection) -> Result<(), String> {
    conn.execute_batch(
        "PRAGMA foreign_keys = ON;
         PRAGMA journal_mode = WAL;",
    )
    .map_err(|e| format!("pragma: {e}"))?;

    conn.execute_batch(MIGRATION_0001)
        .map_err(|e| format!("migration 0001: {e}"))?;

    // 0002 uses ALTER TABLE — skip when column already present (re-open / tests).
    let has_storage_path: bool = conn
        .prepare("PRAGMA table_info(objects)")
        .and_then(|mut stmt| {
            let rows = stmt.query_map([], |row| {
                let name: String = row.get(1)?;
                Ok(name)
            })?;
            for r in rows {
                if r? == "storage_path" {
                    return Ok(true);
                }
            }
            Ok(false)
        })
        .map_err(|e| format!("pragma table_info: {e}"))?;

    if !has_storage_path {
        conn.execute_batch(MIGRATION_0002)
            .map_err(|e| format!("migration 0002: {e}"))?;
    }

    conn.execute_batch(MIGRATION_0003)
        .map_err(|e| format!("migration 0003: {e}"))?;

    conn.execute_batch(MIGRATION_0004)
        .map_err(|e| format!("migration 0004: {e}"))?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::Connection;

    #[test]
    fn migration_creates_tables_storage_path_and_blobs() {
        let conn = Connection::open_in_memory().unwrap();
        apply(&conn).expect("migrate");
        let n: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('buckets','objects','blobs','project_quota','project_usage')",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 5);
        let has: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM pragma_table_info('objects') WHERE name = 'storage_path'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(has, 1);
        // Idempotent re-apply.
        apply(&conn).expect("re-migrate");
    }
}
