//! Brute-force cosine search helpers (normalize, score, top-k, metadata filters).

mod cosine;
mod filter;

pub use cosine::{cosine_dot, l2_normalize, select_topk, ScoredId};
pub use filter::matches_filter;
