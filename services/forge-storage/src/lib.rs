pub mod api;
pub mod backend;
pub mod config;
pub mod health;
pub mod identity;
pub mod meta;
pub mod project;
pub mod state;

use crate::project::require_project;
use crate::state::AppState;
use axum::middleware;
use axum::Router;

/// Build the HTTP application (health + identity + project-scoped bucket APIs).
pub fn app(state: AppState) -> Router {
    let buckets = api::router().layer(middleware::from_fn_with_state(
        state.clone(),
        require_project,
    ));

    Router::new()
        .merge(health::router())
        .merge(buckets)
        .with_state(state)
}
