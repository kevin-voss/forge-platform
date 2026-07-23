pub mod config;
pub mod health;
pub mod state;
pub mod store;

use crate::state::AppState;
use axum::Router;

/// Build the HTTP application (health + identity only in 17.01).
pub fn app(state: AppState) -> Router {
    Router::new().merge(health::router()).with_state(state)
}
