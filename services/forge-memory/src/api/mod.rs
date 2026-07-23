//! HTTP API routes for collections, records, upsert, and query.

mod collections;
mod query;
mod records;
mod upsert;
mod validate;

use crate::state::AppState;
use axum::Router;

pub fn router() -> Router<AppState> {
    Router::new()
        .merge(collections::router())
        .merge(records::router())
        .merge(upsert::router())
        .merge(query::router())
}

pub use validate::{validate_collection_name, validate_record_id};
