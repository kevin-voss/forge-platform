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

/// Outcome of deleting an object (for logging + blob GC).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ObjectDeleteOutcome {
    pub object: ObjectMeta,
    pub refcount_before: i64,
    pub refcount_after: i64,
}

impl ObjectDeleteOutcome {
    /// True when the content-addressed blob is unreferenced and should be unlinked.
    pub fn should_gc_blob(&self) -> bool {
        self.object.sha256.is_some() && self.refcount_after == 0
    }
}

/// Per-project usage vs effective quota.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct UsageReport {
    pub project_id: String,
    pub used_bytes: i64,
    pub quota_bytes: i64,
    pub objects: i64,
}

/// Boot reconcile summary.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReconcileReport {
    pub projects: usize,
    pub blobs: usize,
    /// Relative `storage_path` values that are no longer referenced (GC candidates).
    pub orphan_blob_paths: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MetaError {
    NotFound,
    Conflict(String),
    Invalid(String),
    Internal(String),
    QuotaExceeded { used_bytes: i64, quota_bytes: i64, incoming_bytes: i64 },
}

impl std::fmt::Display for MetaError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound => write!(f, "not found"),
            Self::Conflict(msg) | Self::Invalid(msg) | Self::Internal(msg) => write!(f, "{msg}"),
            Self::QuotaExceeded {
                used_bytes,
                quota_bytes,
                incoming_bytes,
            } => write!(
                f,
                "quota exceeded: used={used_bytes} incoming={incoming_bytes} quota={quota_bytes}"
            ),
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
    ///
    /// Maintains `blobs.refcount` for content-addressed SHA-256 blobs.
    pub fn upsert_object(
        &self,
        project_id: &str,
        bucket_name: &str,
        key: &str,
        size_bytes: i64,
        content_type: Option<&str>,
        storage_path: &str,
        sha256: &str,
    ) -> Result<(ObjectMeta, bool), MetaError> {
        let project_id = require_project(project_id)?;
        if storage_path.trim().is_empty() {
            return Err(MetaError::Invalid("storage_path is required".into()));
        }
        let sha256 = sha256.trim().to_ascii_lowercase();
        if sha256.len() != 64 || !sha256.chars().all(|c| c.is_ascii_hexdigit()) {
            return Err(MetaError::Invalid("sha256 must be 64 hex characters".into()));
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
        let old_sha = existing.as_ref().and_then(|o| o.sha256.clone());
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;

        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin tx: {e}")))?;

        tx.execute(
            "INSERT INTO objects \
             (id, project_id, bucket_id, key, size_bytes, sha256, content_type, storage_path, created_at, updated_at) \
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
             ON CONFLICT(project_id, bucket_id, key) DO UPDATE SET
               size_bytes = excluded.size_bytes,
               sha256 = excluded.sha256,
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

        // Adjust blob refcounts when the key's content hash changes.
        let same_hash = old_sha.as_deref() == Some(sha256.as_str());
        if !same_hash {
            if let Some(old) = old_sha.as_deref() {
                tx.execute(
                    "UPDATE blobs SET refcount = refcount - 1 WHERE sha256 = ?1 AND refcount > 0",
                    params![old],
                )
                .map_err(|e| MetaError::Internal(format!("decrement blob: {e}")))?;
            }
        tx.execute(
            "INSERT INTO blobs (sha256, size_bytes, refcount) VALUES (?1, ?2, 1)
                 ON CONFLICT(sha256) DO UPDATE SET
                   refcount = refcount + 1,
                   size_bytes = excluded.size_bytes",
                params![sha256, size_bytes],
            )
            .map_err(|e| MetaError::Internal(format!("increment blob: {e}")))?;
        }

        let old_size = existing.as_ref().map(|o| o.size_bytes).unwrap_or(0);
        adjust_usage_locked(&tx, project_id, size_bytes - old_size, if created { 1 } else { 0 })?;

        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;

        Ok((
            ObjectMeta {
                id,
                project_id: project_id.to_string(),
                bucket_id: bucket.id,
                key: key.to_string(),
                size_bytes,
                sha256: Some(sha256),
                content_type: content_type.map(str::to_string),
                storage_path: storage_path.to_string(),
                created_at,
                updated_at,
            },
            created,
        ))
    }

    /// Current refcount for a content-addressed blob (0 when absent).
    pub fn blob_refcount(&self, sha256: &str) -> Result<i64, MetaError> {
        let sha256 = sha256.trim().to_ascii_lowercase();
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let n: Option<i64> = conn
            .query_row(
                "SELECT refcount FROM blobs WHERE sha256 = ?1",
                params![sha256],
                |row| row.get(0),
            )
            .optional()
            .map_err(|e| MetaError::Internal(format!("blob refcount: {e}")))?;
        Ok(n.unwrap_or(0))
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

    /// List all objects in a project-scoped bucket (any key).
    pub fn list_objects(
        &self,
        project_id: &str,
        bucket_name: &str,
    ) -> Result<Vec<ObjectMeta>, MetaError> {
        let project_id = require_project(project_id)?;
        let bucket = self.get_bucket(project_id, bucket_name)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare(
                "SELECT id, project_id, bucket_id, key, size_bytes, sha256, content_type, storage_path, created_at, updated_at \
                 FROM objects WHERE project_id = ?1 AND bucket_id = ?2 ORDER BY key ASC",
            )
            .map_err(|e| MetaError::Internal(format!("prepare list objects: {e}")))?;
        let rows = stmt
            .query_map(params![project_id, bucket.id], |row| {
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
            })
            .map_err(|e| MetaError::Internal(format!("list objects: {e}")))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.map_err(|e| MetaError::Internal(format!("row: {e}")))?);
        }
        Ok(out)
    }

    /// Set or replace a per-project quota override (admin/internal).
    pub fn set_project_quota(&self, project_id: &str, quota_bytes: i64) -> Result<(), MetaError> {
        let project_id = require_project(project_id)?;
        if quota_bytes < 0 {
            return Err(MetaError::Invalid("quota_bytes must be non-negative".into()));
        }
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.execute(
            "INSERT INTO project_quota (project_id, quota_bytes) VALUES (?1, ?2)
             ON CONFLICT(project_id) DO UPDATE SET quota_bytes = excluded.quota_bytes",
            params![project_id, quota_bytes],
        )
        .map_err(|e| MetaError::Internal(format!("set quota: {e}")))?;
        Ok(())
    }

    /// Effective quota: override row when present, otherwise `default_quota_bytes`.
    pub fn effective_quota(
        &self,
        project_id: &str,
        default_quota_bytes: u64,
    ) -> Result<i64, MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let override_q: Option<i64> = conn
            .query_row(
                "SELECT quota_bytes FROM project_quota WHERE project_id = ?1",
                params![project_id],
                |row| row.get(0),
            )
            .optional()
            .map_err(|e| MetaError::Internal(format!("get quota: {e}")))?;
        Ok(override_q.unwrap_or(default_quota_bytes as i64))
    }

    /// Incremental usage counters for a project (0/0 when absent).
    pub fn project_usage_counters(&self, project_id: &str) -> Result<(i64, i64), MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let row: Option<(i64, i64)> = conn
            .query_row(
                "SELECT used_bytes, object_count FROM project_usage WHERE project_id = ?1",
                params![project_id],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()
            .map_err(|e| MetaError::Internal(format!("get usage: {e}")))?;
        Ok(row.unwrap_or((0, 0)))
    }

    pub fn project_usage_report(
        &self,
        project_id: &str,
        default_quota_bytes: u64,
    ) -> Result<UsageReport, MetaError> {
        let project_id = require_project(project_id)?;
        let (used_bytes, objects) = self.project_usage_counters(project_id)?;
        let quota_bytes = self.effective_quota(project_id, default_quota_bytes)?;
        Ok(UsageReport {
            project_id: project_id.to_string(),
            used_bytes,
            quota_bytes,
            objects,
        })
    }

    /// Delete one object: remove metadata, decrement blob refcount, drop blob row at 0.
    pub fn delete_object(
        &self,
        project_id: &str,
        bucket_name: &str,
        key: &str,
    ) -> Result<ObjectDeleteOutcome, MetaError> {
        let project_id = require_project(project_id)?;
        let object = self.get_object(project_id, bucket_name, key)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin tx: {e}")))?;

        let n = tx
            .execute(
                "DELETE FROM objects WHERE project_id = ?1 AND id = ?2",
                params![project_id, object.id],
            )
            .map_err(|e| MetaError::Internal(format!("delete object: {e}")))?;
        if n == 0 {
            return Err(MetaError::NotFound);
        }

        let (refcount_before, refcount_after) = if let Some(sha) = object.sha256.as_deref() {
            let before: i64 = tx
                .query_row(
                    "SELECT refcount FROM blobs WHERE sha256 = ?1",
                    params![sha],
                    |row| row.get(0),
                )
                .optional()
                .map_err(|e| MetaError::Internal(format!("blob before: {e}")))?
                .unwrap_or(0);
            tx.execute(
                "UPDATE blobs SET refcount = refcount - 1 WHERE sha256 = ?1 AND refcount > 0",
                params![sha],
            )
            .map_err(|e| MetaError::Internal(format!("decrement blob: {e}")))?;
            let after = (before - 1).max(0);
            if after == 0 {
                tx.execute("DELETE FROM blobs WHERE sha256 = ?1", params![sha])
                    .map_err(|e| MetaError::Internal(format!("delete blob row: {e}")))?;
            }
            (before, after)
        } else {
            (0, 0)
        };

        adjust_usage_locked(&tx, project_id, -object.size_bytes, -1)?;

        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit: {e}")))?;

        Ok(ObjectDeleteOutcome {
            object,
            refcount_before,
            refcount_after,
        })
    }

    /// Cascade-delete all objects in a bucket, then the bucket itself.
    pub fn delete_bucket_cascade(
        &self,
        project_id: &str,
        name: &str,
    ) -> Result<Vec<ObjectDeleteOutcome>, MetaError> {
        let project_id = require_project(project_id)?;
        let objects = self.list_objects(project_id, name)?;
        let mut outcomes = Vec::with_capacity(objects.len());
        for obj in &objects {
            outcomes.push(self.delete_object(project_id, name, &obj.key)?);
        }
        self.delete_bucket(project_id, name)?;
        Ok(outcomes)
    }

    /// Recompute usage counters and blob refcounts from `objects`; return orphan blob paths.
    pub fn reconcile(&self) -> Result<ReconcileReport, MetaError> {
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let tx = conn
            .unchecked_transaction()
            .map_err(|e| MetaError::Internal(format!("begin tx: {e}")))?;

        // Capture previously tracked blobs so we can GC disk orphans after rebuild.
        let mut previous_paths: Vec<String> = Vec::new();
        {
            let mut stmt = tx
                .prepare("SELECT sha256 FROM blobs")
                .map_err(|e| MetaError::Internal(format!("prepare blobs: {e}")))?;
            let rows = stmt
                .query_map([], |row| {
                    let sha: String = row.get(0)?;
                    Ok(sha)
                })
                .map_err(|e| MetaError::Internal(format!("list blobs: {e}")))?;
            for r in rows {
                let sha = r.map_err(|e| MetaError::Internal(format!("row: {e}")))?;
                if sha.len() >= 2 {
                    previous_paths.push(format!("{}/{}", &sha[..2], sha));
                }
            }
        }

        tx.execute("DELETE FROM blobs", [])
            .map_err(|e| MetaError::Internal(format!("clear blobs: {e}")))?;
        tx.execute(
            "INSERT INTO blobs (sha256, size_bytes, refcount)
             SELECT sha256, MAX(size_bytes), COUNT(*)
             FROM objects
             WHERE sha256 IS NOT NULL AND trim(sha256) != ''
             GROUP BY sha256",
            [],
        )
        .map_err(|e| MetaError::Internal(format!("rebuild blobs: {e}")))?;

        tx.execute("DELETE FROM project_usage", [])
            .map_err(|e| MetaError::Internal(format!("clear usage: {e}")))?;
        tx.execute(
            "INSERT INTO project_usage (project_id, used_bytes, object_count)
             SELECT project_id, COALESCE(SUM(size_bytes), 0), COUNT(*)
             FROM objects
             GROUP BY project_id",
            [],
        )
        .map_err(|e| MetaError::Internal(format!("rebuild usage: {e}")))?;

        let mut live_paths: std::collections::HashSet<String> = std::collections::HashSet::new();
        {
            let mut stmt = tx
                .prepare("SELECT sha256 FROM blobs")
                .map_err(|e| MetaError::Internal(format!("prepare live blobs: {e}")))?;
            let rows = stmt
                .query_map([], |row| {
                    let sha: String = row.get(0)?;
                    Ok(sha)
                })
                .map_err(|e| MetaError::Internal(format!("list live blobs: {e}")))?;
            for r in rows {
                let sha = r.map_err(|e| MetaError::Internal(format!("row: {e}")))?;
                if sha.len() >= 2 {
                    live_paths.insert(format!("{}/{}", &sha[..2], sha));
                }
            }
        }

        let blobs = live_paths.len();
        let projects: i64 = tx
            .query_row("SELECT COUNT(*) FROM project_usage", [], |row| row.get(0))
            .map_err(|e| MetaError::Internal(format!("count projects: {e}")))?;

        tx.commit()
            .map_err(|e| MetaError::Internal(format!("commit reconcile: {e}")))?;

        let orphan_blob_paths: Vec<String> = previous_paths
            .into_iter()
            .filter(|p| !live_paths.contains(p))
            .collect();

        Ok(ReconcileReport {
            projects: projects as usize,
            blobs,
            orphan_blob_paths,
        })
    }

    /// All live content-addressed storage paths currently referenced by `blobs`.
    pub fn live_blob_paths(&self) -> Result<std::collections::HashSet<String>, MetaError> {
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        let mut stmt = conn
            .prepare("SELECT sha256 FROM blobs")
            .map_err(|e| MetaError::Internal(format!("prepare: {e}")))?;
        let rows = stmt
            .query_map([], |row| {
                let sha: String = row.get(0)?;
                Ok(sha)
            })
            .map_err(|e| MetaError::Internal(format!("query: {e}")))?;
        let mut out = std::collections::HashSet::new();
        for r in rows {
            let sha = r.map_err(|e| MetaError::Internal(format!("row: {e}")))?;
            if sha.len() >= 2 {
                out.insert(format!("{}/{}", &sha[..2], sha));
            }
        }
        Ok(out)
    }

    /// Test helper: overwrite incremental usage counters without touching objects.
    #[cfg(test)]
    pub fn force_usage_for_test(
        &self,
        project_id: &str,
        used_bytes: i64,
        object_count: i64,
    ) -> Result<(), MetaError> {
        let project_id = require_project(project_id)?;
        let conn = self
            .conn
            .lock()
            .map_err(|_| MetaError::Internal("lock".into()))?;
        conn.execute(
            "INSERT INTO project_usage (project_id, used_bytes, object_count) VALUES (?1, ?2, ?3)
             ON CONFLICT(project_id) DO UPDATE SET
               used_bytes = excluded.used_bytes,
               object_count = excluded.object_count",
            params![project_id, used_bytes, object_count],
        )
        .map_err(|e| MetaError::Internal(format!("force usage: {e}")))?;
        Ok(())
    }
}

fn adjust_usage_locked(
    tx: &rusqlite::Transaction<'_>,
    project_id: &str,
    delta_bytes: i64,
    delta_objects: i64,
) -> Result<(), MetaError> {
    // Ensure a row exists, then apply clamped deltas.
    tx.execute(
        "INSERT OR IGNORE INTO project_usage (project_id, used_bytes, object_count) VALUES (?1, 0, 0)",
        params![project_id],
    )
    .map_err(|e| MetaError::Internal(format!("ensure usage row: {e}")))?;
    tx.execute(
        "UPDATE project_usage SET
           used_bytes = MAX(0, used_bytes + ?2),
           object_count = MAX(0, object_count + ?3)
         WHERE project_id = ?1",
        params![project_id, delta_bytes, delta_objects],
    )
    .map_err(|e| MetaError::Internal(format!("adjust usage: {e}")))?;
    Ok(())
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
            let hash_a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
            let hash_b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
            let (first, created) = s
                .upsert_object(
                    "proj-a",
                    "artifacts",
                    "file.txt",
                    11,
                    Some("text/plain"),
                    "aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                    hash_a,
                )
                .unwrap();
            assert!(created);
            assert_eq!(first.size_bytes, 11);
            assert_eq!(first.content_type.as_deref(), Some("text/plain"));
            assert_eq!(first.sha256.as_deref(), Some(hash_a));
            assert_eq!(s.blob_refcount(hash_a).unwrap(), 1);

            let (second, created) = s
                .upsert_object(
                    "proj-a",
                    "artifacts",
                    "file.txt",
                    22,
                    Some("application/octet-stream"),
                    "bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                    hash_b,
                )
                .unwrap();
            assert!(!created);
            assert_eq!(second.id, first.id);
            assert_eq!(second.created_at, first.created_at);
            assert_eq!(second.size_bytes, 22);
            assert_eq!(second.sha256.as_deref(), Some(hash_b));
            assert_eq!(
                second.content_type.as_deref(),
                Some("application/octet-stream")
            );
            assert_eq!(s.blob_refcount(hash_a).unwrap(), 0);
            assert_eq!(s.blob_refcount(hash_b).unwrap(), 1);
        });
    }

    #[test]
    fn upsert_identical_content_increments_refcount() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            let hash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
            let path = "cc/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
            s.upsert_object("proj-a", "artifacts", "a.bin", 3, None, path, hash)
                .unwrap();
            s.upsert_object("proj-a", "artifacts", "b.bin", 3, None, path, hash)
                .unwrap();
            assert_eq!(s.blob_refcount(hash).unwrap(), 2);
        });
    }

    #[test]
    fn delete_refcount_keeps_shared_blob_then_gcs() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            let hash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd";
            let path = "dd/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd";
            s.upsert_object("proj-a", "artifacts", "a.bin", 4, None, path, hash)
                .unwrap();
            s.upsert_object("proj-a", "artifacts", "b.bin", 4, None, path, hash)
                .unwrap();
            let first = s.delete_object("proj-a", "artifacts", "a.bin").unwrap();
            assert_eq!(first.refcount_before, 2);
            assert_eq!(first.refcount_after, 1);
            assert!(!first.should_gc_blob());
            assert_eq!(s.blob_refcount(hash).unwrap(), 1);

            let second = s.delete_object("proj-a", "artifacts", "b.bin").unwrap();
            assert_eq!(second.refcount_after, 0);
            assert!(second.should_gc_blob());
            assert_eq!(s.blob_refcount(hash).unwrap(), 0);

            let usage = s.project_usage_report("proj-a", 1_000).unwrap();
            assert_eq!(usage.used_bytes, 0);
            assert_eq!(usage.objects, 0);
        });
    }

    #[test]
    fn reconcile_recomputes_usage_from_metadata() {
        with_store(|s| {
            s.create_bucket("proj-a", "artifacts").unwrap();
            let hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
            let path = "ee/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
            s.upsert_object("proj-a", "artifacts", "a.bin", 10, None, path, hash)
                .unwrap();
            s.force_usage_for_test("proj-a", 999, 99).unwrap();
            let report = s.reconcile().unwrap();
            assert_eq!(report.blobs, 1);
            let usage = s.project_usage_report("proj-a", 1_000).unwrap();
            assert_eq!(usage.used_bytes, 10);
            assert_eq!(usage.objects, 1);
        });
    }
}
