pub mod auth;
pub mod config;
pub mod config_store;
pub mod crypto;
pub mod db;
pub mod routes;
pub mod secrets;
pub mod state;

use crate::auth::middleware::enforce;
use crate::state::AppState;
use axum::middleware;
use axum::Router;

/// Build the full HTTP application (health, data-keys, secrets, config + auth).
pub fn app(state: AppState) -> Router {
    let protected = Router::new()
        .merge(secrets::routes::router())
        .merge(config_store::routes::router())
        .route_layer(middleware::from_fn_with_state(state.clone(), enforce));

    Router::new()
        .merge(routes::health::router())
        .merge(routes::identity::router())
        .merge(routes::data_keys::router())
        .merge(protected)
        .with_state(state)
}
