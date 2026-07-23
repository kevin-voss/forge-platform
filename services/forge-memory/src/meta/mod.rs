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
    pub dim: i64,
    pub distance: String,
    pub count: i64,
    pub created_at: String,
}

/// Record metadata (vector payload lives in the mmap file).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct RecordMeta {
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

/// SQLite-backed metadata store. Queries are scoped by `project_id` where relevant.
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
        name: &str,
        dim: i64,
        distance: &str,
    ) -> Result<Collection, MetaError> {
        let project_id = require_project(project_id)?;
        if dim < 1 {
            return Err(MetaError::Invalid("dim must be >= 1".into()));
        }
        let created_at = now_rfc3339();
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        match conn.execute(
            "INSERT INTO collections (name, project_id, dim, distance, count, created_at) \
             VALUES (?1, ?2, ?3, ?4, 0, ?5)",
            params![name, project_id, dim, distance, created_at],
        ) {
            Ok(_) => Ok(Collection {
                name: name.to_string(),
                project_id: project_id.to_string(),
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

    pub fn list_collections(&self, project_id: &str) -> Result<Vec<Collection>, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT name, project_id, dim, distance, count, created_at FROM collections \
                 WHERE project_id = ?1 ORDER BY name ASC",
            )
            .map_err(|e| MetaError::Internal(format!("prepare list: {e}")))?;
        let rows = stmt
            .query_map(params![project_id], |row| {
                Ok(Collection {
                    name: row.get(0)?,
                    project_id: row.get(1)?,
                    dim: row.get(2)?,
                    distance: row.get(3)?,
                    count: row.get(4)?,
                    created_at: row.get(5)?,
                })
            })
            .map_err(|e| MetaError::Internal(format!("list collections: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    pub fn get_collection(&self, project_id: &str, name: &str) -> Result<Collection, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT name, project_id, dim, distance, count, created_at FROM collections \
             WHERE project_id = ?1 AND name = ?2",
            params![project_id, name],
            |row| {
                Ok(Collection {
                    name: row.get(0)?,
                    project_id: row.get(1)?,
                    dim: row.get(2)?,
                    distance: row.get(3)?,
                    count: row.get(4)?,
                    created_at: row.get(5)?,
                })
            },
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get collection: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    pub fn delete_collection(&self, project_id: &str, name: &str) -> Result<(), MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin: {e}")))?;
        let n = tx
            .execute(
                "DELETE FROM collections WHERE project_id = ?1 AND name = ?2",
                params![project_id, name],
            )
            .map_err(|e| MetaError::Internal(format!("delete collection: {e}")))?;
        if n == 0 {
            return Err(MetaError::NotFound);
        }
        // CASCADE should remove records; keep explicit for clarity across SQLite builds.
        tx.execute("DELETE FROM records WHERE collection = ?1", params![name])
            .map_err(|e| MetaError::Internal(format!("delete records: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(())
    }

    pub fn insert_record_meta(
        &self,
        collection: &str,
        id: &str,
        offset: i64,
        metadata: &serde_json::Value,
        document_ref: Option<&str>,
    ) -> Result<RecordMeta, MetaError> {
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
            "INSERT INTO records (collection, id, offset, metadata, document_ref, deleted) \
             VALUES (?1, ?2, ?3, ?4, ?5, 0)",
            params![collection, id, offset, metadata_json, document_ref],
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
            "UPDATE collections SET count = count + 1 WHERE name = ?1",
            params![collection],
        )
        .map_err(|e| MetaError::Internal(format!("bump count: {e}")))?;
        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;
        Ok(RecordMeta {
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
        collection: &str,
        id: &str,
    ) -> Result<RecordMeta, MetaError> {
        // Ensure collection is visible to the project first.
        let _ = self.get_collection(project_id, collection)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT collection, id, offset, metadata, document_ref, deleted FROM records \
             WHERE collection = ?1 AND id = ?2 AND deleted = 0",
            params![collection, id],
            map_record_row,
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get record: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    pub fn list_record_meta(
        &self,
        project_id: &str,
        collection: &str,
        offset: i64,
        limit: i64,
    ) -> Result<Vec<RecordMeta>, MetaError> {
        let _ = self.get_collection(project_id, collection)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT collection, id, offset, metadata, document_ref, deleted FROM records \
                 WHERE collection = ?1 AND deleted = 0 \
                 ORDER BY offset ASC LIMIT ?2 OFFSET ?3",
            )
            .map_err(|e| MetaError::Internal(format!("prepare list records: {e}")))?;
        let rows = stmt
            .query_map(params![collection, limit, offset], map_record_row)
            .map_err(|e| MetaError::Internal(format!("list records: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    pub fn next_vector_offset(&self, collection: &str) -> Result<i64, MetaError> {
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let max: Option<i64> = conn
            .query_row(
                "SELECT MAX(offset) FROM records WHERE collection = ?1",
                params![collection],
                |row| row.get::<_, Option<i64>>(0),
            )
            .map_err(|e| MetaError::Internal(format!("max offset: {e}")))?;
        Ok(max.map(|m| m + 1).unwrap_or(0))
    }
}

fn map_record_row(row: &rusqlite::Row<'_>) -> rusqlite::Result<RecordMeta> {
    let metadata_raw: Option<String> = row.get(3)?;
    let metadata = match metadata_raw {
        Some(s) if !s.is_empty() => serde_json::from_str(&s).unwrap_or(serde_json::Value::Null),
        _ => serde_json::Value::Null,
    };
    let deleted: i64 = row.get(5)?;
    Ok(RecordMeta {
        collection: row.get(0)?,
        id: row.get(1)?,
        offset: row.get(2)?,
        metadata,
        document_ref: row.get(4)?,
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

fn now_rfc3339() -> String {
    chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn collection_crud_and_duplicate() {
        let dir = tempdir().unwrap();
        let store = MetaStore::open(dir.path().join("index.db")).unwrap();
        let c = store
            .create_collection("proj-a", "incidents", 384, "cosine")
            .unwrap();
        assert_eq!(c.dim, 384);
        assert_eq!(c.count, 0);
        let err = store
            .create_collection("proj-a", "incidents", 384, "cosine")
            .unwrap_err();
        assert!(matches!(err, MetaError::Conflict(_)));
        let got = store.get_collection("proj-a", "incidents").unwrap();
        assert_eq!(got.name, "incidents");
        store.delete_collection("proj-a", "incidents").unwrap();
        assert!(matches!(
            store.get_collection("proj-a", "incidents"),
            Err(MetaError::NotFound)
        ));
    }
}
