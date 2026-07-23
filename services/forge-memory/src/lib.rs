pub mod acl;
pub mod api;
pub mod collections;
pub mod config;
pub mod health;
pub mod identity;
pub mod meta;
pub mod scope;
pub mod search;
pub mod state;
pub mod store;
pub mod vectors;

use crate::scope::require_project;
use crate::state::AppState;
use axum::middleware;
use axum::Router;

/// Build the HTTP application (health + identity + project-scoped collection/record APIs).
pub fn app(state: AppState) -> Router {
    let api = api::router().layer(middleware::from_fn_with_state(
        state.clone(),
        require_project,
    ));

    Router::new()
        .merge(health::router())
        .merge(api)
        .with_state(state)
}
