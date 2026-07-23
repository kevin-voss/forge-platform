pub mod backend;
pub mod config;
pub mod health;
pub mod state;

use crate::state::AppState;
use axum::Router;

/// Build the HTTP application (health + identity). Object APIs arrive in later steps.
pub fn app(state: AppState) -> Router {
    Router::new().merge(health::router()).with_state(state)
}
