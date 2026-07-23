//! Project-scoped metadata index backed by SQLite under `meta/`.

mod migrations;

use migrations::apply;
use rusqlite::{params, Connection, OptionalExtension};
use serde::Serialize;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use uuid::Uuid;

/// A project-scoped bucket record.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct Bucket {
    pub id: String,
    pub project_id: String,
    pub name: String,
    pub created_at: String,
}

/// Object metadata (payload bytes on disk via `storage_path`).
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct ObjectMeta {
    pub id: String,
    pub project_id: String,
    pub bucket_id: String,
    pub key: String,
    pub size_bytes: i64,
    pub sha256: Option<String>,
    pub content_type: Option<String>,
    pub storage_path: String,
    pub created_at: String,
    pub updated_at: String,
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

/// SQLite-backed metadata store. Every query is scoped by `project_id`.
pub struct MetadataStore {
    path: PathBuf,
    conn: Mutex<Connection>,
}

impl MetadataStore {
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

    pub fn create_bucket(&self, project_id: &str, name: &str) -> Result<Bucket, MetaError> {
        let project_id = require_project(project_id)?;
        let id = Uuid::new_v4().to_string();
        let created_at = now_rfc3339();
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        match conn.execute(
            "INSERT INTO buckets (id, project_id, name, created_at) VALUES (?1, ?2, ?3, ?4)",
            params![id, project_id, name, created_at],
        ) {
            Ok(_) => Ok(Bucket {
                id,
                project_id: project_id.to_string(),
                name: name.to_string(),
                created_at,
            }),
            Err(rusqlite::Error::SqliteFailure(err, _))
                if err.code == rusqlite::ErrorCode::ConstraintViolation =>
            {
                Err(MetaError::Conflict(format!(
                    "bucket {name:?} already exists in project"
                )))
            }
            Err(e) => Err(MetaError::Internal(format!("insert bucket: {e}"))),
        }
    }

    pub fn list_buckets(&self, project_id: &str) -> Result<Vec<Bucket>, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT id, project_id, name, created_at FROM buckets \
                 WHERE project_id = ?1 ORDER BY name ASC",
            )
            .map_err(|e| MetaError::Internal(format!("prepare list: {e}")))?;
        let rows = stmt
            .query_map(params![project_id], |row| {
                Ok(Bucket {
                    id: row.get(0)?,
                    project_id: row.get(1)?,
                    name: row.get(2)?,
                    created_at: row.get(3)?,
                })
            })
            .map_err(|e| MetaError::Internal(format!("list buckets: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    pub fn get_bucket(&self, project_id: &str, name: &str) -> Result<Bucket, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT id, project_id, name, created_at FROM buckets \
             WHERE project_id = ?1 AND name = ?2",
            params![project_id, name],
            |row| {
                Ok(Bucket {
                    id: row.get(0)?,
                    project_id: row.get(1)?,
                    name: row.get(2)?,
                    created_at: row.get(3)?,
                })
            },
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get bucket: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    /// Delete an empty bucket. Returns `Conflict` with object count when non-empty.
    pub fn delete_bucket(&self, project_id: &str, name: &str) -> Result<(), MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let bucket_id: String = conn
            .query_row(
                "SELECT id FROM buckets WHERE project_id = ?1 AND name = ?2",
                params![project_id, name],
                |row| row.get(0),
            )
            .optional()
            .map_err(|e| MetaError::Internal(format!("lookup bucket: {e}")))?
            .ok_or(MetaError::NotFound)?;

        let count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM objects WHERE project_id = ?1 AND bucket_id = ?2",
                params![project_id, bucket_id],
                |row| row.get(0),
            )
            .map_err(|e| MetaError::Internal(format!("count objects: {e}")))?;
        if count > 0 {
            return Err(MetaError::Conflict(format!(
                "bucket not empty: {count} object(s)"
            )));
        }

        let n = conn
            .execute(
                "DELETE FROM buckets WHERE project_id = ?1 AND id = ?2",
                params![project_id, bucket_id],
            )
            .map_err(|e| MetaError::Internal(format!("delete bucket: {e}")))?;
        if n == 0 {
            return Err(MetaError::NotFound);
        }
        Ok(())
    }

    /// Insert a placeholder object metadata row (no payload bytes).
    pub fn insert_object_placeholder(
        &self,
        project_id: &str,
        bucket_name: &str,
        key: &str,
    ) -> Result<ObjectMeta, MetaError> {
        let project_id = require_project(project_id)?;
        let bucket = self.get_bucket(project_id, bucket_name)?;
        let id = Uuid::new_v4().to_string();
        let now = now_rfc3339();
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        match conn.execute(
            "INSERT INTO objects \
             (id, project_id, bucket_id, key, size_bytes, sha256, content_type, storage_path, created_at, updated_at) \
             VALUES (?1, ?2, ?3, ?4, 0, NULL, NULL, '', ?5, ?5)",
            params![id, project_id, bucket.id, key, now],
        ) {
            Ok(_) => Ok(ObjectMeta {
                id,
                project_id: project_id.to_string(),
                bucket_id: bucket.id,
                key: key.to_string(),
                size_bytes: 0,
                sha256: None,
                content_type: None,
                storage_path: String::new(),
                created_at: now.clone(),
                updated_at: now,
            }),
            Err(rusqlite::Error::SqliteFailure(err, _))
                if err.code == rusqlite::ErrorCode::ConstraintViolation =>
            {
                Err(MetaError::Conflict(format!(
                    "object key {key:?} already exists"
                )))
            }
            Err(e) => Err(MetaError::Internal(format!("insert object: {e}"))),
        }
    }

    /// Upsert object metadata after a successful streamed upload.
    /// Returns `(meta, created)` where `created` is true when the key was new.
    pub fn upsert_object(
        &self,
        project_id: &str,
        bucket_name: &str,
        key: &str,
        size_bytes: i64,
        content_type: Option<&str>,
        storage_path: &str,
    ) -> Result<(ObjectMeta, bool), MetaError> {
        let project_id = require_project(project_id)?;
        if storage_path.trim().is_empty() {
            return Err(MetaError::Invalid("storage_path is required".into()));
        }
        let bucket = self.get_bucket(project_id, bucket_name)?;
        let existing = self
            .get_object(project_id, bucket_name, key)
            .map(Some)
            .or_else(|e| match e {
                MetaError::NotFound => Ok(None),
                other => Err(other),
            })?;
        let created = existing.is_none();
        let id = existing
            .as_ref()
            .map(|o| o.id.clone())
            .unwrap_or_else(|| Uuid::new_v4().to_string());
        let created_at = existing
            .as_ref()
            .map(|o| o.created_at.clone())
            .unwrap_or_else(now_rfc3339);
        let updated_at = now_rfc3339();
        let sha256 = existing.as_ref().and_then(|o| o.sha256.clone());
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.execute(
            "INSERT INTO objects \
             (id, project_id, bucket_id, key, size_bytes, sha256, content_type, storage_path, created_at, updated_at) \
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
             ON CONFLICT(project_id, bucket_id, key) DO UPDATE SET
               size_bytes = excluded.size_bytes,
               content_type = excluded.content_type,
               storage_path = excluded.storage_path,
               updated_at = excluded.updated_at",
            params![
                id,
                project_id,
                bucket.id,
                key,
                size_bytes,
                sha256,
                content_type,
                storage_path,
                created_at,
                updated_at
            ],
        )
        .map_err(|e| MetaError::Internal(format!("upsert object: {e}")))?;
        Ok((
            ObjectMeta {
                id,
                project_id: project_id.to_string(),
                bucket_id: bucket.id,
                key: key.to_string(),
                size_bytes,
                sha256,
                content_type: content_type.map(str::to_string),
                storage_path: storage_path.to_string(),
                created_at,
                updated_at,
            },
            created,
        ))
    }

    pub fn get_object(
        &self,
        project_id: &str,
        bucket_name: &str,
        key: &str,
    ) -> Result<ObjectMeta, MetaError> {
        let project_id = require_project(project_id)?;
        let bucket = self.get_bucket(project_id, bucket_name)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT id, project_id, bucket_id, key, size_bytes, sha256, content_type, storage_path, created_at, updated_at \
             FROM objects WHERE project_id = ?1 AND bucket_id = ?2 AND key = ?3",
            params![project_id, bucket.id, key],
            |row| {
                Ok(ObjectMeta {
                    id: row.get(0)?,
                    project_id: row.get(1)?,
                    bucket_id: row.get(2)?,
                    key: row.get(3)?,
                    size_bytes: row.get(4)?,
                    sha256: row.get(5)?,
                    content_type: row.get(6)?,
                    storage_path: row.get(7)?,
                    created_at: row.get(8)?,
                    updated_at: row.get(9)?,
                })
            },
        )
        .optional()
        .map_err(|e| MetaError::Internal(format!("get object: {e}")))?
        .ok_or(MetaError::NotFound)
    }

    pub fn count_objects(&self, project_id: &str, bucket_name: &str) -> Result<i64, MetaError> {
        let project_id = require_project(project_id)?;
        let bucket = self.get_bucket(project_id, bucket_name)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.query_row(
            "SELECT COUNT(*) FROM objects WHERE project_id = ?1 AND bucket_id = ?2",
            params![project_id, bucket.id],
            |row| row.get(0),
        )
        .map_err(|e| MetaError::Internal(format!("count: {e}")))
    }
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

    fn with_store<F: FnOnce(&MetadataStore)>(f: F) {
        let dir = tempdir().unwrap();
        let path = dir.path().join("meta").join("index.db");
        let s = MetadataStore::open(path).expect("open");
        f(&s);
    }

    #[test]
    fn create_list_get_delete_happy_path() {
        with_store(|s| {
            let b = s.create_bucket("proj-a", "artifacts").unwrap();
            assert_eq!(b.name, "artifacts");
            assert_eq!(b.project_id, "proj-a");

            let listed = s.list_buckets("proj-a").unwrap();
            assert_eq!(listed.len(), 1);
            assert_eq!(listed[0].name, "artifacts");

            let got = s.get_bucket("proj-a", "artifacts").unwrap();
            assert_eq!(got.id, b.id);

            s.delete_bucket("proj-a", "artifacts").unwrap();
            assert!(matches!(
                s.get_bucket("proj-a", "artifacts"),
                Err(MetaError::NotFound)
            ));
        });
    }

    #[test]
    fn cross_project_query_returns_empty_or_not_found() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            assert!(s.list_buckets("proj-b").unwrap().is_empty());
            assert!(matches!(
                s.get_bucket("proj-b", "artifacts"),
                Err(MetaError::NotFound)
            ));
            assert!(matches!(
                s.delete_bucket("proj-b", "artifacts"),
                Err(MetaError::NotFound)
            ));
        });
    }

    #[test]
    fn duplicate_bucket_conflicts() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            assert!(matches!(
                s.create_bucket("proj-a", "artifacts"),
                Err(MetaError::Conflict(_))
            ));
            // Same name in another project is fine.
            s.create_bucket("proj-b", "artifacts").unwrap();
        });
    }

    #[test]
    fn delete_non_empty_conflicts() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            s.insert_object_placeholder("proj-a", "artifacts", "file.txt")
                .unwrap();
            match s.delete_bucket("proj-a", "artifacts") {
                Err(MetaError::Conflict(msg)) => assert!(msg.contains("1"), "{msg}"),
                other => panic!("expected conflict, got {other:?}"),
            }
        });
    }

    #[test]
    fn object_invisible_across_projects() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            s.insert_object_placeholder("proj-a", "artifacts", "file.txt")
                .unwrap();
            assert!(matches!(
                s.get_object("proj-b", "artifacts", "file.txt"),
                Err(MetaError::NotFound)
            ));
        });
    }

    #[test]
    fn upsert_object_creates_and_overwrites() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            let (first, created) = s
                .upsert_object(
                    "proj-a",
                    "artifacts",
                    "file.txt",
                    11,
                    Some("text/plain"),
                    "proj-a/bucket/abc",
                )
                .unwrap();
            assert!(created);
            assert_eq!(first.size_bytes, 11);
            assert_eq!(first.content_type.as_deref(), Some("text/plain"));
            assert_eq!(first.storage_path, "proj-a/bucket/abc");

            let (second, created) = s
                .upsert_object(
                    "proj-a",
                    "artifacts",
                    "file.txt",
                    22,
                    Some("application/octet-stream"),
                    "proj-a/bucket/abc",
                )
                .unwrap();
            assert!(!created);
            assert_eq!(second.id, first.id);
            assert_eq!(second.created_at, first.created_at);
            assert_eq!(second.size_bytes, 22);
            assert_eq!(
                second.content_type.as_deref(),
                Some("application/octet-stream")
            );
        });
    }
}
