//! HTTP API handlers (buckets + streamed objects + signed tokens + usage).

pub mod buckets;
pub mod objects;
pub mod sign;
pub mod usage;
pub mod validate;

use crate::state::AppState;
use axum::Router;

pub fn router() -> Router<AppState> {
    buckets::router()
        .merge(objects::router())
        .merge(usage::router())
}
