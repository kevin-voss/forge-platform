//! SQLite metadata index under `meta/index.db`.

mod migrations;

use migrations::apply;
use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

/// Collection metadata row.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Collection {
    pub name: String,
    pub project_id: String,
    pub namespace: String,
    pub dim: i64,
    pub distance: String,
    pub count: i64,
    pub created_at: String,
}

/// Record metadata (vector payload lives in the mmap file).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct RecordMeta {
    pub project_id: String,
    pub namespace: String,
    pub collection: String,
    pub id: String,
    pub offset: i64,
    pub metadata: serde_json::Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub document_ref: Option<String>,
    pub deleted: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MetaError {
    NotFound,
    Conflict(String),
    Invalid(String),
    Internal(String),
}

impl std::fmt::Display for MetaError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound => write!(f, "not found"),
            Self::Conflict(msg) | Self::Invalid(msg) | Self::Internal(msg) => write!(f, "{msg}"),
        }
    }
}

impl std::error::Error for MetaError {}

/// SQLite-backed metadata store. Every query is scoped by `project_id` (+ namespace).
pub struct MetaStore {
    path: PathBuf,
    conn: Mutex<Connection>,
}

impl MetaStore {
    /// Open (or create) the metadata database at `path` and run migrations.
    pub fn open(path: impl Into<PathBuf>) -> Result<Self, String> {
        let path = path.into();
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .map_err(|e| format!("create meta dir {}: {e}", parent.display()))?;
        }
        let conn = Connection::open(&path).map_err(|e| format!("open {}: {e}", path.display()))?;
        apply(&conn)?;
        Ok(Self {
            path,
            conn: Mutex::new(conn),
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn create_collection(
        &self,
        project_id: &str,
        namespace: &str,
        name: &str,
        dim: i64,
        distance: &str,
    ) -> Result<Collection, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        if dim < 1 {
            return Err(MetaError::Invalid("dim must be >= 1".into()));
        }
        let created_at = now_rfc3339();
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        match conn.execute(
            "INSERT INTO collections (project_id, namespace, name, dim, distance, count, created_at) \
             VALUES (?1, ?2, ?3, ?4, ?5, 0, ?6)",
            params![project_id, namespace, name, dim, distance, created_at],
        ) {
            Ok(_) => Ok(Collection {
                name: name.to_string(),
                project_id: project_id.to_string(),
                namespace: namespace.to_string(),
                dim,
                distance: distance.to_string(),
                count: 0,
                created_at,
            }),
            Err(rusqlite::Error::SqliteFailure(err, _))
                if err.code == rusqlite::ErrorCode::ConstraintViolation =>
            {
                Err(MetaError::Conflict(format!(
                    "collection {name:?} already exists"
                )))
            }
            Err(e) => Err(MetaError::Internal(format!("insert collection: {e}"))),
        }
    }

    pub fn list_collections(
        &self,
        project_id: &str,
        namespace: Option<&str>,
    ) -> Result<Vec<Collection>, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut out = Vec::new();
        if let Some(ns) = namespace {
            let ns = normalize_ns(ns);
            let mut stmt = conn
                .prepare(
                    "SELECT name, project_id, namespace, dim, distance, count, created_at \
                     FROM collections WHERE project_id = ?1 AND namespace = ?2 ORDER BY name ASC",
                )
                .map_err(|e| MetaError::Internal(format!("prepare list: {e}")))?;
            let rows = stmt
                .query_map(params![project_id, ns], map_collection_row)
                .map_err(|e| MetaError::Internal(format!("list collections: {e}")))?;
            for r in rows {
                out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
            }
        } else {
            let mut stmt = conn
                .prepare(
                    "SELECT name, project_id, namespace, dim, distance, count, created_at \
                     FROM collections WHERE project_id = ?1 ORDER BY namespace ASC, name ASC",
                )
                .map_err(|e| MetaError::Internal(format!("prepare list: {e}")))?;
            let rows = stmt
                .query_map(params![project_id], map_collection_row)
                .map_err(|e| MetaError::Internal(format!("list collections: {e}")))?;
            for r in rows {
                out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
            }
        }
        Ok(out)
    }

    pub fn get_collection(
        &self,
        project_id: &str,
        namespace: &str,
        name: &str,
    ) -> Result<Collection, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT name, project_id, namespace, dim, distance, count, created_at FROM collections \
             WHERE project_id = ?1 AND namespace = ?2 AND name = ?3",
            params![project_id, namespace, name],
            map_collection_row,
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get collection: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    pub fn delete_collection(
        &self,
        project_id: &str,
        namespace: &str,
        name: &str,
    ) -> Result<(), MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;
        let n = tx
            .execute(
                "DELETE FROM collections WHERE project_id = ?1 AND namespace = ?2 AND name = ?3",
                params![project_id, namespace, name],
            )
            .map_err(|e| MetaError::Internal(format!("delete collection: {e}")))?;
        if n == 0 {
            return Err(MetaError::NotFound);
        }
        tx.execute(
            "DELETE FROM records WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3",
            params![project_id, namespace, name],
        )
        .map_err(|e| MetaError::Internal(format!("delete records: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(())
    }

    pub fn insert_record_meta(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        id: &str,
        offset: i64,
        metadata: &serde_json::Value,
        document_ref: Option<&str>,
    ) -> Result<RecordMeta, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let metadata_json = serde_json::to_string(metadata)
            .map_err(|e| MetaError::Invalid(format!("metadata json: {e}")))?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;
        match tx.execute(
            "INSERT INTO records (project_id, namespace, collection, id, offset, metadata, document_ref, deleted) \
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, 0)",
            params![project_id, namespace, collection, id, offset, metadata_json, document_ref],
        ) {
            Ok(_) => {}
            Err(rusqlite::Error::SqliteFailure(err, _))
                if err.code == rusqlite::ErrorCode::ConstraintViolation =>
            {
                return Err(MetaError::Conflict(format!(
                    "record {id:?} already exists in collection {collection:?}"
                )));
            }
            Err(e) => return Err(MetaError::Internal(format!("insert record: {e}"))),
        }
        tx.execute(
            "UPDATE collections SET count = count + 1 \
             WHERE project_id = ?1 AND namespace = ?2 AND name = ?3",
            params![project_id, namespace, collection],
        )
        .map_err(|e| MetaError::Internal(format!("bump count: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(RecordMeta {
            project_id: project_id.to_string(),
            namespace: namespace.to_string(),
            collection: collection.to_string(),
            id: id.to_string(),
            offset,
            metadata: metadata.clone(),
            document_ref: document_ref.map(str::to_string),
            deleted: false,
        })
    }

    pub fn get_record_meta(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        id: &str,
    ) -> Result<RecordMeta, MetaError> {
        let _ = self.get_collection(project_id, namespace, collection)?;
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT project_id, namespace, collection, id, offset, metadata, document_ref, deleted \
             FROM records WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND id = ?4 AND deleted = 0",
            params![project_id, namespace, collection, id],
            map_record_row,
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get record: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    pub fn list_record_meta(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        offset: i64,
        limit: i64,
    ) -> Result<Vec<RecordMeta>, MetaError> {
        let _ = self.get_collection(project_id, namespace, collection)?;
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT project_id, namespace, collection, id, offset, metadata, document_ref, deleted \
                 FROM records WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND deleted = 0 \
                 ORDER BY offset ASC LIMIT ?4 OFFSET ?5",
            )
            .map_err(|e| MetaError::Internal(format!("prepare list records: {e}")))?;
        let rows = stmt
            .query_map(
                params![project_id, namespace, collection, limit, offset],
                map_record_row,
            )
            .map_err(|e| MetaError::Internal(format!("list records: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    pub fn next_vector_offset(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
    ) -> Result<i64, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let max: Option<i64> = conn
            .query_row(
                "SELECT MAX(offset) FROM records \
                 WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3",
                params![project_id, namespace, collection],
                |row| row.get::<_, Option<i64>>(0),
            )
            .map_err(|e| MetaError::Internal(format!("max offset: {e}")))?;
        Ok(max.map(|m| m + 1).unwrap_or(0))
    }

    pub fn get_record_meta_any(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        id: &str,
    ) -> Result<Option<RecordMeta>, MetaError> {
        let _ = self.get_collection(project_id, namespace, collection)?;
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT project_id, namespace, collection, id, offset, metadata, document_ref, deleted \
             FROM records WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND id = ?4",
            params![project_id, namespace, collection, id],
            map_record_row,
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get record any: {e}")))
    }

    pub fn upsert_record_meta(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        id: &str,
        offset: i64,
        metadata: &serde_json::Value,
        document_ref: Option<&str>,
    ) -> Result<(RecordMeta, bool), MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let metadata_json = serde_json::to_string(metadata)
            .map_err(|e| MetaError::Invalid(format!("metadata json: {e}")))?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;

        let prior: Option<(i64, i64)> = tx
            .query_row(
                "SELECT deleted, offset FROM records \
                 WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND id = ?4",
                params![project_id, namespace, collection, id],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()
            .map_err(|e| MetaError::Internal(format!("lookup prior: {e}")))?;

        let count_delta = match prior {
            None => 1i64,
            Some((deleted, _)) if deleted != 0 => 1,
            Some(_) => 0,
        };

        tx.execute(
            "INSERT INTO records (project_id, namespace, collection, id, offset, metadata, document_ref, deleted) \
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, 0) \
             ON CONFLICT(project_id, namespace, collection, id) DO UPDATE SET \
               offset = excluded.offset, \
               metadata = excluded.metadata, \
               document_ref = excluded.document_ref, \
               deleted = 0",
            params![project_id, namespace, collection, id, offset, metadata_json, document_ref],
        )
        .map_err(|e| MetaError::Internal(format!("upsert record: {e}")))?;

        if count_delta != 0 {
            tx.execute(
                "UPDATE collections SET count = count + ?1 \
                 WHERE project_id = ?2 AND namespace = ?3 AND name = ?4",
                params![count_delta, project_id, namespace, collection],
            )
            .map_err(|e| MetaError::Internal(format!("bump count: {e}")))?;
        }

        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;

        Ok((
            RecordMeta {
                project_id: project_id.to_string(),
                namespace: namespace.to_string(),
                collection: collection.to_string(),
                id: id.to_string(),
                offset,
                metadata: metadata.clone(),
                document_ref: document_ref.map(str::to_string),
                deleted: false,
            },
            count_delta != 0,
        ))
    }

    pub fn tombstone_record(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        id: &str,
    ) -> Result<bool, MetaError> {
        let _ = self.get_collection(project_id, namespace, collection)?;
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;
        let n = tx
            .execute(
                "UPDATE records SET deleted = 1 \
                 WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND id = ?4 AND deleted = 0",
                params![project_id, namespace, collection, id],
            )
            .map_err(|e| MetaError::Internal(format!("tombstone: {e}")))?;
        if n == 0 {
            return Err(MetaError::NotFound);
        }
        tx.execute(
            "UPDATE collections SET count = MAX(count - 1, 0) \
             WHERE project_id = ?1 AND namespace = ?2 AND name = ?3",
            params![project_id, namespace, collection],
        )
        .map_err(|e| MetaError::Internal(format!("decrement count: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(true)
    }

    pub fn list_all_live_record_meta(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
    ) -> Result<Vec<RecordMeta>, MetaError> {
        let _ = self.get_collection(project_id, namespace, collection)?;
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT project_id, namespace, collection, id, offset, metadata, document_ref, deleted \
                 FROM records WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND deleted = 0 \
                 ORDER BY offset ASC",
            )
            .map_err(|e| MetaError::Internal(format!("prepare live records: {e}")))?;
        let rows = stmt
            .query_map(params![project_id, namespace, collection], map_record_row)
            .map_err(|e| MetaError::Internal(format!("list live records: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    pub fn count_tombstoned(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
    ) -> Result<usize, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let n: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM records \
                 WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND deleted = 1",
                params![project_id, namespace, collection],
                |row| row.get(0),
            )
            .map_err(|e| MetaError::Internal(format!("count tombstones: {e}")))?;
        Ok(n as usize)
    }

    pub fn apply_compaction(
        &self,
        project_id: &str,
        namespace: &str,
        collection: &str,
        live: &[RecordMeta],
    ) -> Result<usize, MetaError> {
        let project_id = require_project(project_id)?;
        let namespace = normalize_ns(namespace);
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;
        let removed = tx
            .execute(
                "DELETE FROM records \
                 WHERE project_id = ?1 AND namespace = ?2 AND collection = ?3 AND deleted = 1",
                params![project_id, namespace, collection],
            )
            .map_err(|e| MetaError::Internal(format!("delete tombstones: {e}")))?;
        for rec in live {
            tx.execute(
                "UPDATE records SET offset = ?1 \
                 WHERE project_id = ?2 AND namespace = ?3 AND collection = ?4 AND id = ?5 AND deleted = 0",
                params![rec.offset, project_id, namespace, collection, rec.id],
            )
            .map_err(|e| MetaError::Internal(format!("update offset: {e}")))?;
        }
        tx.execute(
            "UPDATE collections SET count = ?1 \
             WHERE project_id = ?2 AND namespace = ?3 AND name = ?4",
            params![live.len() as i64, project_id, namespace, collection],
        )
        .map_err(|e| MetaError::Internal(format!("set count: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(removed)
    }

    pub fn list_all_collections(&self) -> Result<Vec<Collection>, MetaError> {
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT name, project_id, namespace, dim, distance, count, created_at FROM collections \
                 ORDER BY project_id ASC, namespace ASC, name ASC",
            )
            .map_err(|e| MetaError::Internal(format!("prepare all collections: {e}")))?;
        let rows = stmt
            .query_map([], map_collection_row)
            .map_err(|e| MetaError::Internal(format!("list all collections: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }
}

fn map_collection_row(row: &rusqlite::Row<'_>) -> rusqlite::Result<Collection> {
    Ok(Collection {
        name: row.get(0)?,
        project_id: row.get(1)?,
        namespace: row.get(2)?,
        dim: row.get(3)?,
        distance: row.get(4)?,
        count: row.get(5)?,
        created_at: row.get(6)?,
    })
}

fn map_record_row(row: &rusqlite::Row<'_>) -> rusqlite::Result<RecordMeta> {
    let metadata_raw: Option<String> = row.get(5)?;
    let metadata = match metadata_raw {
        Some(s) if !s.is_empty() => serde_json::from_str(&s).unwrap_or(serde_json::Value::Null),
        _ => serde_json::Value::Null,
    };
    let deleted: i64 = row.get(7)?;
    Ok(RecordMeta {
        project_id: row.get(0)?,
        namespace: row.get(1)?,
        collection: row.get(2)?,
        id: row.get(3)?,
        offset: row.get(4)?,
        metadata,
        document_ref: row.get(6)?,
        deleted: deleted != 0,
    })
}

fn require_project(project_id: &str) -> Result<&str, MetaError> {
    let trimmed = project_id.trim();
    if trimmed.is_empty() {
        return Err(MetaError::Invalid("project_id is required".into()));
    }
    Ok(trimmed)
}

fn normalize_ns(namespace: &str) -> &str {
    namespace.trim()
}

fn now_rfc3339() -> String {
    chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn collection_crud_and_cross_project_independence() {
        let dir = tempdir().unwrap();
        let store = MetaStore::open(dir.path().join("index.db")).unwrap();
        let c = store
            .create_collection("proj-a", "", "incidents", 384, "cosine")
            .unwrap();
        assert_eq!(c.dim, 384);
        assert_eq!(c.namespace, "");
        store
            .create_collection("proj-b", "", "incidents", 64, "cosine")
            .unwrap();
        let err = store
            .create_collection("proj-a", "", "incidents", 384, "cosine")
            .unwrap_err();
        assert!(matches!(err, MetaError::Conflict(_)));
        assert_eq!(store.get_collection("proj-a", "", "incidents").unwrap().dim, 384);
        assert_eq!(store.get_collection("proj-b", "", "incidents").unwrap().dim, 64);
        assert!(matches!(
            store.get_collection("proj-a", "docs", "incidents"),
            Err(MetaError::NotFound)
        ));
        store
            .create_collection("proj-a", "docs", "incidents", 8, "cosine")
            .unwrap();
        assert_eq!(
            store.get_collection("proj-a", "docs", "incidents").unwrap().dim,
            8
        );
        store.delete_collection("proj-a", "", "incidents").unwrap();
        assert!(matches!(
            store.get_collection("proj-a", "", "incidents"),
            Err(MetaError::NotFound)
        ));
        // Other project untouched.
        assert!(store.get_collection("proj-b", "", "incidents").is_ok());
    }
}
