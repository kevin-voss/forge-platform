//! L2 normalization, cosine (normalized dot), and bounded top-k selection.

use std::cmp::Ordering;
use std::collections::BinaryHeap;

/// Scored candidate used by top-k selection.
#[derive(Debug, Clone, PartialEq)]
pub struct ScoredId {
    pub id: String,
    pub score: f32,
}

#[derive(Debug, Clone)]
struct HeapEntry {
    score: f32,
    id: String,
}

impl PartialEq for HeapEntry {
    fn eq(&self, other: &Self) -> bool {
        self.score == other.score && self.id == other.id
    }
}

impl Eq for HeapEntry {}

impl PartialOrd for HeapEntry {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for HeapEntry {
    fn cmp(&self, other: &Self) -> Ordering {
        // Invert score so BinaryHeap (max-heap) surfaces the worst (lowest) score
        // at peek(). On score ties, larger id is worse (evicted first).
        match other
            .score
            .partial_cmp(&self.score)
            .unwrap_or(Ordering::Equal)
        {
            Ordering::Equal => self.id.cmp(&other.id),
            ord => ord,
        }
    }
}

/// L2-normalize a vector. Zero vectors are returned unchanged (scores will be 0).
pub fn l2_normalize(v: &[f32]) -> Vec<f32> {
    let mut sum_sq = 0.0f64;
    for x in v {
        let xf = f64::from(*x);
        sum_sq += xf * xf;
    }
    if sum_sq == 0.0 {
        return v.to_vec();
    }
    let inv = 1.0 / sum_sq.sqrt();
    v.iter().map(|x| (*x as f64 * inv) as f32).collect()
}

/// Cosine similarity of two already L2-normalized vectors (dot product).
pub fn cosine_dot(a: &[f32], b: &[f32]) -> f32 {
    debug_assert_eq!(a.len(), b.len());
    let mut acc = 0.0f64;
    for (x, y) in a.iter().zip(b.iter()) {
        acc += f64::from(*x) * f64::from(*y);
    }
    acc as f32
}

/// Keep the top `k` highest-scoring `(id, score)` pairs; result sorted score desc, then id asc.
pub fn select_topk(candidates: impl IntoIterator<Item = (String, f32)>, k: usize) -> Vec<ScoredId> {
    if k == 0 {
        return Vec::new();
    }
    let mut heap: BinaryHeap<HeapEntry> = BinaryHeap::new();
    for (id, score) in candidates {
        if !score.is_finite() {
            continue;
        }
        if heap.len() < k {
            heap.push(HeapEntry { score, id });
            continue;
        }
        if let Some(peek) = heap.peek() {
            let better =
                score > peek.score || (score == peek.score && id.as_str() < peek.id.as_str());
            if better {
                heap.pop();
                heap.push(HeapEntry { score, id });
            }
        }
    }
    let mut out: Vec<ScoredId> = heap
        .into_iter()
        .map(|e| ScoredId {
            id: e.id,
            score: e.score,
        })
        .collect();
    out.sort_by(
        |a, b| match b.score.partial_cmp(&a.score).unwrap_or(Ordering::Equal) {
            Ordering::Equal => a.id.cmp(&b.id),
            ord => ord,
        },
    );
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn identical_normalized_cosine_is_one() {
        let a = l2_normalize(&[3.0, 4.0]);
        let b = l2_normalize(&[3.0, 4.0]);
        let s = cosine_dot(&a, &b);
        assert!((s - 1.0).abs() < 1e-5, "score={s}");
    }

    #[test]
    fn orthogonal_normalized_cosine_is_zero() {
        let a = l2_normalize(&[1.0, 0.0]);
        let b = l2_normalize(&[0.0, 1.0]);
        let s = cosine_dot(&a, &b);
        assert!(s.abs() < 1e-5, "score={s}");
    }

    #[test]
    fn topk_ordering_on_fixture() {
        let ranked = select_topk(
            [
                ("c".into(), 0.5),
                ("a".into(), 0.9),
                ("b".into(), 0.7),
                ("d".into(), 0.1),
            ],
            3,
        );
        assert_eq!(
            ranked.iter().map(|r| r.id.as_str()).collect::<Vec<_>>(),
            vec!["a", "b", "c"]
        );
        assert!((ranked[0].score - 0.9).abs() < 1e-6);
    }

    #[test]
    fn topk_tie_break_by_id() {
        let ranked = select_topk([("b".into(), 0.5), ("a".into(), 0.5), ("c".into(), 0.5)], 2);
        assert_eq!(
            ranked.iter().map(|r| r.id.as_str()).collect::<Vec<_>>(),
            vec!["a", "b"]
        );
    }
}
