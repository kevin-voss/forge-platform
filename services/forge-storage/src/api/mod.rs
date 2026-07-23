//! HTTP API handlers (buckets in 13.02; objects in later steps).

pub mod buckets;
pub mod validate;

use crate::state::AppState;
use axum::Router;

pub fn router() -> Router<AppState> {
    buckets::router()
}
