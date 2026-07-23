//! CollectionStore: collection CRUD + record insert/upsert/query/delete over MetaStore + VectorFile.

use crate::meta::{Collection, MetaError, MetaStore, RecordMeta};
use crate::search::{cosine_dot, l2_normalize, matches_filter, select_topk};
use crate::vectors::{self, remove_file, VectorFile, VectorFileError};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Instant;
use tracing::{info, warn};

/// Full record returned by read APIs.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Record {
    pub id: String,
    pub vector: Vec<f32>,
    pub metadata: serde_json::Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub document_ref: Option<String>,
}

/// Single nearest-neighbor hit.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct QueryHit {
    pub id: String,
    pub score: f32,
    pub metadata: serde_json::Value,
}

/// Query result plus observability counters.
#[derive(Debug, Clone)]
pub struct QueryOutcome {
    pub results: Vec<QueryHit>,
    pub candidates_scanned: usize,
    pub latency: std::time::Duration,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CollectionError {
    NotFound,
    Conflict(String),
    Invalid(String),
    DimensionMismatch { expected: usize, got: usize },
    Internal(String),
    Corrupt(String),
}

impl std::fmt::Display for CollectionError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound => write!(f, "not found"),
            Self::Conflict(msg) | Self::Invalid(msg) | Self::Internal(msg) | Self::Corrupt(msg) => {
                write!(f, "{msg}")
            }
            Self::DimensionMismatch { expected, got } => {
                write!(
                    f,
                    "vector dimension mismatch: expected {expected}, got {got}"
                )
            }
        }
    }
}

impl std::error::Error for CollectionError {}

impl From<MetaError> for CollectionError {
    fn from(value: MetaError) -> Self {
        match value {
            MetaError::NotFound => Self::NotFound,
            MetaError::Conflict(m) => Self::Conflict(m),
            MetaError::Invalid(m) => Self::Invalid(m),
            MetaError::Internal(m) => Self::Internal(m),
        }
    }
}

impl From<VectorFileError> for CollectionError {
    fn from(value: VectorFileError) -> Self {
        match value {
            VectorFileError::DimensionMismatch { expected, got } => {
                Self::DimensionMismatch { expected, got }
            }
            VectorFileError::Corrupt(m) => Self::Corrupt(m),
            VectorFileError::Io(e) => Self::Internal(e.to_string()),
            VectorFileError::OutOfBounds { offset, len } => {
                Self::Internal(format!("vector offset {offset} out of bounds (len={len})"))
            }
        }
    }
}

/// Orchestrates SQLite metadata and per-collection mmap vector files.
pub struct CollectionStore {
    meta: Arc<MetaStore>,
    vectors_dir: PathBuf,
    max_dim: usize,
    max_metadata_bytes: usize,
    files: Mutex<HashMap<String, Arc<Mutex<VectorFile>>>>,
}

impl CollectionStore {
    pub fn new(
        meta: Arc<MetaStore>,
        vectors_dir: PathBuf,
        max_dim: usize,
        max_metadata_bytes: usize,
    ) -> Self {
        Self {
            meta,
            vectors_dir,
            max_dim: max_dim.max(1),
            max_metadata_bytes: max_metadata_bytes.max(1),
            files: Mutex::new(HashMap::new()),
        }
    }

    pub fn meta(&self) -> &MetaStore {
        &self.meta
    }

    fn vec_path(&self, name: &str) -> PathBuf {
        self.vectors_dir.join(format!("{name}.vec"))
    }

    pub(crate) fn open_vector_file(
        &self,
        name: &str,
        dim: usize,
    ) -> Result<Arc<Mutex<VectorFile>>, CollectionError> {
        let mut guard = self
            .files
            .lock()
            .map_err(|_| CollectionError::Internal("files lock".into()))?;
        if let Some(existing) = guard.get(name) {
            return Ok(Arc::clone(existing));
        }
        let path = self.vec_path(name);
        let vf = VectorFile::open(&path, dim)?;
        let arc = Arc::new(Mutex::new(vf));
        guard.insert(name.to_string(), Arc::clone(&arc));
        Ok(arc)
    }

    fn drop_vector_file(&self, name: &str) {
        if let Ok(mut guard) = self.files.lock() {
            guard.remove(name);
        }
        if let Err(e) = remove_file(&self.vec_path(name)) {
            warn!(collection = %name, error = %e, "failed to remove vector file");
        }
    }

    pub fn create_collection(
        &self,
        project_id: &str,
        name: &str,
        dim: i64,
        distance: &str,
    ) -> Result<Collection, CollectionError> {
        if dim < 1 || dim as usize > self.max_dim {
            return Err(CollectionError::Invalid(format!(
                "dim must be between 1 and {}",
                self.max_dim
            )));
        }
        if distance != "cosine" {
            return Err(CollectionError::Invalid(
                "distance must be \"cosine\"".into(),
            ));
        }
        let collection = self
            .meta
            .create_collection(project_id, name, dim, distance)?;
        // Touch/create empty vector file so corrupt detection applies on reopen.
        let _ = self.open_vector_file(name, dim as usize)?;
        info!(
            project_id = %project_id,
            collection = %name,
            dim,
            distance,
            "collection created"
        );
        Ok(collection)
    }

    pub fn list_collections(&self, project_id: &str) -> Result<Vec<Collection>, CollectionError> {
        Ok(self.meta.list_collections(project_id)?)
    }

    pub fn get_collection(
        &self,
        project_id: &str,
        name: &str,
    ) -> Result<Collection, CollectionError> {
        Ok(self.meta.get_collection(project_id, name)?)
    }

    pub fn delete_collection(&self, project_id: &str, name: &str) -> Result<(), CollectionError> {
        let existing = self.meta.get_collection(project_id, name)?;
        self.meta.delete_collection(project_id, name)?;
        self.drop_vector_file(name);
        info!(
            project_id = %project_id,
            collection = %name,
            count = existing.count,
            "collection deleted"
        );
        Ok(())
    }

    fn validate_vector_and_metadata(
        &self,
        expected_dim: usize,
        vector: &[f32],
        metadata: &serde_json::Value,
    ) -> Result<(), CollectionError> {
        if vector.len() != expected_dim {
            return Err(CollectionError::DimensionMismatch {
                expected: expected_dim,
                got: vector.len(),
            });
        }
        let meta_bytes = serde_json::to_vec(metadata)
            .map_err(|e| CollectionError::Invalid(format!("metadata json: {e}")))?;
        if meta_bytes.len() > self.max_metadata_bytes {
            return Err(CollectionError::Invalid(format!(
                "metadata exceeds {} bytes",
                self.max_metadata_bytes
            )));
        }
        Ok(())
    }

    /// Insert-storage primitive (no normalize). Prefer [`Self::upsert_record`] for HTTP.
    pub fn insert_record(
        &self,
        project_id: &str,
        collection: &str,
        id: &str,
        vector: &[f32],
        metadata: serde_json::Value,
        document_ref: Option<String>,
    ) -> Result<Record, CollectionError> {
        let col = self.meta.get_collection(project_id, collection)?;
        let expected = col.dim as usize;
        self.validate_vector_and_metadata(expected, vector, &metadata)?;

        let vf = self.open_vector_file(collection, expected)?;
        let offset = {
            let mut file = vf
                .lock()
                .map_err(|_| CollectionError::Internal("vector lock".into()))?;
            let next = self.meta.next_vector_offset(collection)?;
            file.write_at(next as u64, vector)?;
            next
        };

        let _meta = self.meta.insert_record_meta(
            collection,
            id,
            offset,
            &metadata,
            document_ref.as_deref(),
        )?;

        Ok(Record {
            id: id.to_string(),
            vector: vector.to_vec(),
            metadata,
            document_ref,
        })
    }

    /// Upsert by id: L2-normalize, overwrite existing slot or append, upsert metadata.
    pub fn upsert_record(
        &self,
        project_id: &str,
        collection: &str,
        id: &str,
        vector: &[f32],
        metadata: serde_json::Value,
        document_ref: Option<String>,
    ) -> Result<Record, CollectionError> {
        let col = self.meta.get_collection(project_id, collection)?;
        let expected = col.dim as usize;
        self.validate_vector_and_metadata(expected, vector, &metadata)?;

        let normalized = l2_normalize(vector);
        let prior = self.meta.get_record_meta_any(project_id, collection, id)?;
        let offset = match &prior {
            Some(existing) => existing.offset,
            None => self.meta.next_vector_offset(collection)?,
        };

        let vf = self.open_vector_file(collection, expected)?;
        {
            let mut file = vf
                .lock()
                .map_err(|_| CollectionError::Internal("vector lock".into()))?;
            file.write_at(offset as u64, &normalized)?;
        }

        self.meta
            .upsert_record_meta(collection, id, offset, &metadata, document_ref.as_deref())?;

        Ok(Record {
            id: id.to_string(),
            vector: normalized,
            metadata,
            document_ref,
        })
    }

    /// Batch upsert; returns number of records written.
    pub fn upsert_batch(
        &self,
        project_id: &str,
        collection: &str,
        records: &[(String, Vec<f32>, serde_json::Value, Option<String>)],
    ) -> Result<usize, CollectionError> {
        // Ensure collection exists once up front.
        let _ = self.meta.get_collection(project_id, collection)?;
        let mut n = 0usize;
        for (id, vector, metadata, document_ref) in records {
            self.upsert_record(
                project_id,
                collection,
                id,
                vector,
                metadata.clone(),
                document_ref.clone(),
            )?;
            n += 1;
        }
        Ok(n)
    }

    /// Brute-force cosine NN over live records with optional metadata filter.
    pub fn query(
        &self,
        project_id: &str,
        collection: &str,
        vector: &[f32],
        top_k: usize,
        filter: Option<&serde_json::Value>,
    ) -> Result<QueryOutcome, CollectionError> {
        let started = Instant::now();
        let col = self.meta.get_collection(project_id, collection)?;
        let expected = col.dim as usize;
        if vector.len() != expected {
            return Err(CollectionError::DimensionMismatch {
                expected,
                got: vector.len(),
            });
        }
        let query_norm = l2_normalize(vector);
        let live = self
            .meta
            .list_all_live_record_meta(project_id, collection)?;
        let vf = self.open_vector_file(collection, expected)?;
        let file = vf
            .lock()
            .map_err(|_| CollectionError::Internal("vector lock".into()))?;

        let mut scored: Vec<(String, f32, serde_json::Value)> = Vec::new();
        let mut candidates = 0usize;
        for meta in live {
            if !matches_filter(filter, &meta.metadata) {
                continue;
            }
            candidates += 1;
            let raw = file.read_at(meta.offset as u64)?;
            let cand_norm = l2_normalize(&raw);
            if cand_norm.len() != expected {
                return Err(CollectionError::Corrupt(format!(
                    "vector at offset {} has dim {}, expected {}",
                    meta.offset,
                    cand_norm.len(),
                    expected
                )));
            }
            let score = cosine_dot(&query_norm, &cand_norm);
            scored.push((meta.id, score, meta.metadata));
        }

        let top = select_topk(scored.iter().map(|(id, s, _)| (id.clone(), *s)), top_k);
        let mut results = Vec::with_capacity(top.len());
        for s in top {
            let metadata = scored
                .iter()
                .find(|(id, _, _)| id == &s.id)
                .map(|(_, _, m)| m.clone())
                .unwrap_or(serde_json::Value::Null);
            results.push(QueryHit {
                id: s.id,
                score: s.score,
                metadata,
            });
        }

        Ok(QueryOutcome {
            results,
            candidates_scanned: candidates,
            latency: started.elapsed(),
        })
    }

    /// Tombstone a record (excluded from subsequent queries).
    pub fn delete_record(
        &self,
        project_id: &str,
        collection: &str,
        id: &str,
    ) -> Result<(), CollectionError> {
        self.meta.tombstone_record(project_id, collection, id)?;
        info!(
            project_id = %project_id,
            collection = %collection,
            record_id = %id,
            "record tombstoned"
        );
        Ok(())
    }

    pub fn compact_collection(
        &self,
        project_id: &str,
        name: &str,
    ) -> Result<usize, CollectionError> {
        vectors::compact_collection(self, project_id, name)
    }

    pub fn compact_all(&self) -> Result<usize, CollectionError> {
        vectors::compact_all(self)
    }

    pub fn get_record(
        &self,
        project_id: &str,
        collection: &str,
        id: &str,
    ) -> Result<Record, CollectionError> {
        let col = self.meta.get_collection(project_id, collection)?;
        let meta: RecordMeta = self.meta.get_record_meta(project_id, collection, id)?;
        let vf = self.open_vector_file(collection, col.dim as usize)?;
        let vector = {
            let file = vf
                .lock()
                .map_err(|_| CollectionError::Internal("vector lock".into()))?;
            file.read_at(meta.offset as u64)?
        };
        Ok(Record {
            id: meta.id,
            vector,
            metadata: meta.metadata,
            document_ref: meta.document_ref,
        })
    }

    pub fn list_records(
        &self,
        project_id: &str,
        collection: &str,
        offset: i64,
        limit: i64,
    ) -> Result<Vec<Record>, CollectionError> {
        let col = self.meta.get_collection(project_id, collection)?;
        let metas = self
            .meta
            .list_record_meta(project_id, collection, offset, limit)?;
        let vf = self.open_vector_file(collection, col.dim as usize)?;
        let file = vf
            .lock()
            .map_err(|_| CollectionError::Internal("vector lock".into()))?;
        let mut out = Vec::with_capacity(metas.len());
        for meta in metas {
            let vector = file.read_at(meta.offset as u64)?;
            out.push(Record {
                id: meta.id,
                vector,
                metadata: meta.metadata,
                document_ref: meta.document_ref,
            });
        }
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    fn store(dir: &std::path::Path) -> CollectionStore {
        let meta = Arc::new(MetaStore::open(dir.join("meta/index.db")).unwrap());
        CollectionStore::new(meta, dir.join("vectors"), 4096, 65_536)
    }

    #[test]
    fn insert_rejects_dimension_mismatch() {
        let dir = tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
        let s = store(dir.path());
        s.create_collection("proj-a", "incidents", 4, "cosine")
            .unwrap();
        let err = s
            .insert_record(
                "proj-a",
                "incidents",
                "r1",
                &[1.0, 2.0],
                serde_json::json!({"k": "v"}),
                None,
            )
            .unwrap_err();
        assert!(matches!(
            err,
            CollectionError::DimensionMismatch {
                expected: 4,
                got: 2
            }
        ));
    }

    #[test]
    fn insert_get_and_restart_persist() {
        let dir = tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
        let meta_path = dir.path().join("meta/index.db");
        let vectors = dir.path().join("vectors");
        {
            let meta = Arc::new(MetaStore::open(&meta_path).unwrap());
            let s = CollectionStore::new(Arc::clone(&meta), vectors.clone(), 4096, 65_536);
            s.create_collection("proj-a", "incidents", 3, "cosine")
                .unwrap();
            s.insert_record(
                "proj-a",
                "incidents",
                "r1",
                &[0.1, 0.2, 0.3],
                serde_json::json!({"type": "deploy"}),
                Some("storage://bucket/obj".into()),
            )
            .unwrap();
        }
        let meta = Arc::new(MetaStore::open(&meta_path).unwrap());
        let s = CollectionStore::new(meta, vectors, 4096, 65_536);
        let col = s.get_collection("proj-a", "incidents").unwrap();
        assert_eq!(col.dim, 3);
        assert_eq!(col.count, 1);
        let rec = s.get_record("proj-a", "incidents", "r1").unwrap();
        assert_eq!(rec.vector, vec![0.1, 0.2, 0.3]);
        assert_eq!(rec.metadata["type"], "deploy");
        assert_eq!(rec.document_ref.as_deref(), Some("storage://bucket/obj"));
    }

    #[test]
    fn upsert_updates_existing_id_in_place() {
        let dir = tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
        let s = store(dir.path());
        s.create_collection("proj-a", "incidents", 2, "cosine")
            .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "r1",
            &[1.0, 0.0],
            serde_json::json!({"v": 1}),
            None,
        )
        .unwrap();
        let before = s
            .meta()
            .get_record_meta("proj-a", "incidents", "r1")
            .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "r1",
            &[0.0, 1.0],
            serde_json::json!({"v": 2}),
            None,
        )
        .unwrap();
        let after = s
            .meta()
            .get_record_meta("proj-a", "incidents", "r1")
            .unwrap();
        assert_eq!(before.offset, after.offset);
        assert_eq!(after.metadata["v"], 2);
        let col = s.get_collection("proj-a", "incidents").unwrap();
        assert_eq!(col.count, 1);
        let rec = s.get_record("proj-a", "incidents", "r1").unwrap();
        // Stored L2-normalized.
        assert!((rec.vector[0] - 0.0).abs() < 1e-5);
        assert!((rec.vector[1] - 1.0).abs() < 1e-5);
    }

    #[test]
    fn query_ranks_known_fixture() {
        let dir = tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
        let s = store(dir.path());
        s.create_collection("proj-a", "incidents", 2, "cosine")
            .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "east",
            &[1.0, 0.0],
            serde_json::json!({"type": "deploy"}),
            None,
        )
        .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "north",
            &[0.0, 1.0],
            serde_json::json!({"type": "alert"}),
            None,
        )
        .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "diag",
            &[0.8, 0.2],
            serde_json::json!({"type": "deploy"}),
            None,
        )
        .unwrap();

        let out = s
            .query("proj-a", "incidents", &[1.0, 0.0], 2, None)
            .unwrap();
        assert_eq!(out.results.len(), 2);
        assert_eq!(out.results[0].id, "east");
        assert!((out.results[0].score - 1.0).abs() < 1e-5);
        assert_eq!(out.results[1].id, "diag");

        let filtered = s
            .query(
                "proj-a",
                "incidents",
                &[1.0, 0.0],
                5,
                Some(&serde_json::json!({"type": "deploy"})),
            )
            .unwrap();
        assert_eq!(filtered.results.len(), 2);
        assert!(filtered
            .results
            .iter()
            .all(|h| h.metadata["type"] == "deploy"));
    }

    #[test]
    fn delete_excludes_then_compaction_reclaims() {
        let dir = tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
        let s = store(dir.path());
        s.create_collection("proj-a", "incidents", 2, "cosine")
            .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "keep",
            &[1.0, 0.0],
            serde_json::json!({}),
            None,
        )
        .unwrap();
        s.upsert_record(
            "proj-a",
            "incidents",
            "drop",
            &[0.0, 1.0],
            serde_json::json!({}),
            None,
        )
        .unwrap();
        s.delete_record("proj-a", "incidents", "drop").unwrap();
        let out = s
            .query("proj-a", "incidents", &[1.0, 0.0], 5, None)
            .unwrap();
        assert_eq!(out.results.len(), 1);
        assert_eq!(out.results[0].id, "keep");
        assert_eq!(s.meta().count_tombstoned("incidents").unwrap(), 1);
        let removed = s.compact_collection("proj-a", "incidents").unwrap();
        assert_eq!(removed, 1);
        assert_eq!(s.meta().count_tombstoned("incidents").unwrap(), 0);
        let vf = s.open_vector_file("incidents", 2).unwrap();
        assert_eq!(vf.lock().unwrap().len(), 1);
    }
}
