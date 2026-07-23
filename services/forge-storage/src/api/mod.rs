//! HTTP API handlers (buckets + streamed objects).

pub mod buckets;
pub mod objects;
pub mod validate;

use crate::state::AppState;
use axum::Router;

pub fn router() -> Router<AppState> {
    buckets::router().merge(objects::router())
}
