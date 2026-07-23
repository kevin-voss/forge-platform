//! Tombstone compaction: rewrite live vectors contiguously and drop deleted rows.

use crate::collections::{CollectionError, CollectionStore};
use crate::meta::RecordMeta;
use tracing::info;

/// Compact one collection: reclaim vector slots held by tombstones.
///
/// Returns the number of tombstoned rows removed.
pub fn compact_collection(
    store: &CollectionStore,
    project_id: &str,
    namespace: &str,
    name: &str,
) -> Result<usize, CollectionError> {
    let col = store.get_collection(project_id, namespace, name)?;
    let dim = col.dim as usize;
    let live = store
        .meta()
        .list_all_live_record_meta(project_id, namespace, name)?;

    let vf = store.open_vector_file(project_id, namespace, name, dim)?;
    let mut payloads: Vec<(RecordMeta, Vec<f32>)> = Vec::with_capacity(live.len());
    {
        let file = vf
            .lock()
            .map_err(|_| CollectionError::Internal("vector lock".into()))?;
        for meta in &live {
            let vector = file.read_at(meta.offset as u64)?;
            payloads.push((meta.clone(), vector));
        }
    }

    let live_count = payloads.len();
    {
        let mut file = vf
            .lock()
            .map_err(|_| CollectionError::Internal("vector lock".into()))?;
        for (i, (_meta, vector)) in payloads.iter().enumerate() {
            file.write_at(i as u64, vector)?;
        }
        file.truncate(live_count as u64)?;
    }

    let mut new_metas = Vec::with_capacity(live_count);
    for (i, (meta, _)) in payloads.into_iter().enumerate() {
        new_metas.push(RecordMeta {
            project_id: meta.project_id,
            namespace: meta.namespace,
            collection: meta.collection,
            id: meta.id,
            offset: i as i64,
            metadata: meta.metadata,
            document_ref: meta.document_ref,
            deleted: false,
        });
    }

    let removed = store
        .meta()
        .apply_compaction(project_id, namespace, name, &new_metas)?;
    info!(
        project_id = %project_id,
        namespace = %namespace,
        collection = %name,
        live = live_count,
        tombstones_removed = removed,
        "collection compacted"
    );
    Ok(removed)
}

/// Compact every collection visible in the metadata index.
pub fn compact_all(store: &CollectionStore) -> Result<usize, CollectionError> {
    let cols = store.meta().list_all_collections()?;
    let mut total = 0usize;
    for c in cols {
        total += compact_collection(store, &c.project_id, &c.namespace, &c.name)?;
    }
    Ok(total)
}
