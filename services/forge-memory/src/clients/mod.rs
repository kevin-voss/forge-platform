//! Outbound HTTP clients (Models embeddings).

pub mod models;

pub use models::{
    EmbedResult, FakeModelsClient, HttpModelsClient, ModelsClient, ModelsClientError,
};
