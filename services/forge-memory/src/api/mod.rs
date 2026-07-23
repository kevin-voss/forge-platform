//! HTTP API routes for collections and records.

mod collections;
mod records;
mod validate;

use crate::state::AppState;
use axum::Router;

pub fn router() -> Router<AppState> {
    Router::new()
        .merge(collections::router())
        .merge(records::router())
}

pub use validate::{validate_collection_name, validate_record_id};
