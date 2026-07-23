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

/// Object metadata placeholder (payload bytes arrive in 13.03).
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct ObjectMeta {
    pub id: String,
    pub project_id: String,
    pub bucket_id: String,
    pub key: String,
    pub size_bytes: i64,
    pub sha256: Option<String>,
    pub content_type: Option<String>,
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
             (id, project_id, bucket_id, key, size_bytes, sha256, content_type, created_at, updated_at) \
             VALUES (?1, ?2, ?3, ?4, 0, NULL, NULL, ?5, ?5)",
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
            "SELECT id, project_id, bucket_id, key, size_bytes, sha256, content_type, created_at, updated_at \
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
                    created_at: row.get(7)?,
                    updated_at: row.get(8)?,
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
}
