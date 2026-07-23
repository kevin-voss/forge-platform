//! SQLite schema migrations for the metadata index.

/// Embedded initial schema (source: `migrations/0001_collections.sql`).
pub const MIGRATION_0001: &str = include_str!("../../migrations/0001_collections.sql");

/// Namespace column + composite uniqueness (source: `migrations/0002_namespace.sql`).
pub const MIGRATION_0002: &str = include_str!("../../migrations/0002_namespace.sql");

/// Apply all migrations to an open connection.
pub fn apply(conn: &rusqlite::Connection) -> Result<(), String> {
    conn.execute_batch(
        "PRAGMA foreign_keys = ON;
         PRAGMA journal_mode = WAL;",
    )
    .map_err(|e| format!("pragma: {e}"))?;

    conn.execute_batch(MIGRATION_0001)
        .map_err(|e| format!("migration 0001: {e}"))?;

    // 0002 rebuilds tables — skip when namespace column already present.
    let has_namespace: bool = conn
        .prepare("PRAGMA table_info(collections)")
        .and_then(|mut stmt| {
            let rows = stmt.query_map([], |row| {
                let name: String = row.get(1)?;
                Ok(name)
            })?;
            for r in rows {
                if r? == "namespace" {
                    return Ok(true);
                }
            }
            Ok(false)
        })
        .map_err(|e| format!("pragma table_info: {e}"))?;

    if !has_namespace {
        conn.execute_batch(MIGRATION_0002)
            .map_err(|e| format!("migration 0002: {e}"))?;
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::Connection;

    #[test]
    fn migration_creates_namespaced_schema() {
        let conn = Connection::open_in_memory().unwrap();
        apply(&conn).expect("migrate");
        let n: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('collections','records')",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 2);
        let has: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM pragma_table_info('collections') WHERE name = 'namespace'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(has, 1);
        // Idempotent re-apply.
        apply(&conn).expect("re-migrate");
    }
}
