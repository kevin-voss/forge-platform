//! SQLite schema migrations for the metadata index.

/// Embedded initial schema (source: `migrations/0001_collections.sql`).
pub const MIGRATION_0001: &str = include_str!("../../migrations/0001_collections.sql");

/// Apply all migrations to an open connection.
pub fn apply(conn: &rusqlite::Connection) -> Result<(), String> {
    conn.execute_batch(
        "PRAGMA foreign_keys = ON;
         PRAGMA journal_mode = WAL;",
    )
    .map_err(|e| format!("pragma: {e}"))?;

    conn.execute_batch(MIGRATION_0001)
        .map_err(|e| format!("migration 0001: {e}"))?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::Connection;

    #[test]
    fn migration_creates_collections_and_records() {
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
    }
}
