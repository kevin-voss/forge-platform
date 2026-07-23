//! CollectionStore: collection CRUD + record insert/read over MetaStore + VectorFile.

use crate::meta::{Collection, MetaError, MetaStore, RecordMeta};
use crate::vectors::{remove_file, VectorFile, VectorFileError};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
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

    fn open_vector_file(
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

    /// Insert-storage primitive (HTTP upsert lands in 17.03). Enforces dimension + metadata size.
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
        if vector.len() != expected {
            return Err(CollectionError::DimensionMismatch {
                expected,
                got: vector.len(),
            });
        }
        let meta_bytes = serde_json::to_vec(&metadata)
            .map_err(|e| CollectionError::Invalid(format!("metadata json: {e}")))?;
        if meta_bytes.len() > self.max_metadata_bytes {
            return Err(CollectionError::Invalid(format!(
                "metadata exceeds {} bytes",
                self.max_metadata_bytes
            )));
        }

        let vf = self.open_vector_file(collection, expected)?;
        let offset = {
            let mut file = vf
                .lock()
                .map_err(|_| CollectionError::Internal("vector lock".into()))?;
            // Prefer contiguous append; also tolerate holes via next_vector_offset.
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
}
