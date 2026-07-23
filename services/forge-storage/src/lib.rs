pub mod api;
pub mod backend;
pub mod config;
pub mod health;
pub mod http;
pub mod identity;
pub mod integrity;
pub mod meta;
pub mod project;
pub mod quota;
pub mod signing;
pub mod state;

use crate::project::require_project;
use crate::state::AppState;
use axum::middleware;
use axum::Router;

/// Build the HTTP application (health + identity + project-scoped bucket/object APIs).
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
