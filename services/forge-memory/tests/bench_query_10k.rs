//! Fixture-scale benchmark: brute-force cosine query at N=10_000 (dim=32).
//!
//! Documents wall time in test output; README records a representative number.

use forge_memory::collections::CollectionStore;
use forge_memory::meta::MetaStore;
use std::sync::Arc;
use std::time::Instant;
use tempfile::tempdir;

#[test]
fn brute_force_query_10k_fixture_scale() {
    let dir = tempdir().unwrap();
    std::fs::create_dir_all(dir.path().join("vectors")).unwrap();
    let meta = Arc::new(MetaStore::open(dir.path().join("meta/index.db")).unwrap());
    let store = CollectionStore::new(meta, dir.path().join("vectors"), 4096, 65_536);
    let dim = 32usize;
    store
        .create_collection("proj-a", "bench", dim as i64, "cosine")
        .unwrap();

    let n = 10_000usize;
    let seed_axis = 0usize;
    for i in 0..n {
        let mut v = vec![0.0f32; dim];
        // Spread vectors across axes with a small unique component for ranking stability.
        let axis = i % dim;
        v[axis] = 1.0;
        v[seed_axis] += (i as f32) * 1e-6;
        store
            .upsert_record(
                "proj-a",
                "bench",
                &format!("r{i}"),
                &v,
                serde_json::json!({"i": i}),
                None,
            )
            .unwrap();
    }

    let mut q = vec![0.0f32; dim];
    q[seed_axis] = 1.0;

    let started = Instant::now();
    let out = store.query("proj-a", "bench", &q, 10, None).unwrap();
    let elapsed = started.elapsed();

    assert_eq!(out.results.len(), 10);
    assert_eq!(out.candidates_scanned, n);
    // Nearest should be among vectors dominated by axis 0.
    assert!(out.results[0].score > 0.9);

    eprintln!(
        "forge-memory bench N={n} dim={dim}: query_latency={:?} candidates={}",
        elapsed, out.candidates_scanned
    );
    // Soft upper bound — catches pathological regressions without enforcing a hard SLA.
    assert!(
        elapsed.as_secs_f64() < 30.0,
        "query at N=10k took {:?}, expected < 30s",
        elapsed
    );
}
