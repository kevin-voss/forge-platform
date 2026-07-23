//! Forge Models `/embed` client for text upsert/query convenience paths.

use serde::Deserialize;
use std::sync::Arc;
use std::time::Duration;
use tracing::{info, warn};

/// Successful embedding response from Models.
#[derive(Debug, Clone)]
pub struct EmbedResult {
    pub model: String,
    pub dim: usize,
    pub embeddings: Vec<Vec<f32>>,
}

/// Models client failures (mapped to API error codes by callers).
#[derive(Debug)]
pub enum ModelsClientError {
    Unavailable(String),
    BadResponse(String),
}

impl std::fmt::Display for ModelsClientError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unavailable(msg) => write!(f, "{msg}"),
            Self::BadResponse(msg) => write!(f, "{msg}"),
        }
    }
}

impl std::error::Error for ModelsClientError {}

#[async_trait::async_trait]
pub trait ModelsClient: Send + Sync {
    async fn embed(
        &self,
        model: &str,
        texts: &[String],
        project_id: Option<&str>,
    ) -> Result<EmbedResult, ModelsClientError>;
}

#[derive(Debug, Deserialize)]
struct EmbedResponseBody {
    model: Option<String>,
    dim: Option<usize>,
    embeddings: Option<Vec<Vec<f32>>>,
}

/// HTTP client for `POST /v1/models/{model}/embed`.
pub struct HttpModelsClient {
    base: String,
    http: reqwest::Client,
}

impl HttpModelsClient {
    pub fn new(models_url: &str, timeout: Duration) -> Result<Arc<Self>, String> {
        let http = reqwest::Client::builder()
            .timeout(timeout)
            .connect_timeout(Duration::from_secs(2).min(timeout))
            .build()
            .map_err(|e| format!("models http client: {e}"))?;
        Ok(Arc::new(Self {
            base: models_url.trim_end_matches('/').to_string(),
            http,
        }))
    }
}

#[async_trait::async_trait]
impl ModelsClient for HttpModelsClient {
    async fn embed(
        &self,
        model: &str,
        texts: &[String],
        project_id: Option<&str>,
    ) -> Result<EmbedResult, ModelsClientError> {
        if texts.is_empty() {
            return Err(ModelsClientError::BadResponse(
                "embed texts must not be empty".into(),
            ));
        }
        let url = format!("{}/v1/models/{}/embed", self.base, model);
        let started = std::time::Instant::now();
        let mut req = self.http.post(&url).json(&serde_json::json!({
            "input": texts,
        }));
        if let Some(pid) = project_id.map(str::trim).filter(|s| !s.is_empty()) {
            req = req.header("X-Forge-Project", pid);
        }

        let resp = match req.send().await {
            Ok(r) => r,
            Err(err) => {
                warn!(error = %err, model, "models embed request failed");
                return Err(ModelsClientError::Unavailable(format!(
                    "embedding backend unavailable: {err}"
                )));
            }
        };

        let status = resp.status();
        let body_bytes = match resp.bytes().await {
            Ok(b) => b,
            Err(err) => {
                return Err(ModelsClientError::Unavailable(format!(
                    "embedding backend unavailable: {err}"
                )));
            }
        };

        if status.is_server_error() || status.as_u16() == 502 || status.as_u16() == 503 {
            let msg = String::from_utf8_lossy(&body_bytes);
            return Err(ModelsClientError::Unavailable(format!(
                "embedding backend unavailable: HTTP {status}: {msg}"
            )));
        }
        if !status.is_success() {
            let msg = String::from_utf8_lossy(&body_bytes);
            return Err(ModelsClientError::BadResponse(format!(
                "models embed failed: HTTP {status}: {msg}"
            )));
        }

        let parsed: EmbedResponseBody = serde_json::from_slice(&body_bytes).map_err(|e| {
            ModelsClientError::BadResponse(format!("models embed invalid JSON: {e}"))
        })?;
        let embeddings = parsed.embeddings.unwrap_or_default();
        if embeddings.len() != texts.len() {
            return Err(ModelsClientError::BadResponse(format!(
                "models embed returned {} vectors for {} texts",
                embeddings.len(),
                texts.len()
            )));
        }
        let dim = parsed
            .dim
            .or_else(|| embeddings.first().map(|v| v.len()))
            .unwrap_or(0);
        if dim == 0 {
            return Err(ModelsClientError::BadResponse(
                "models embed returned empty dim".into(),
            ));
        }
        for (i, vec) in embeddings.iter().enumerate() {
            if vec.len() != dim {
                return Err(ModelsClientError::BadResponse(format!(
                    "models embed vector[{i}] dim {} != {dim}",
                    vec.len()
                )));
            }
        }

        let latency_ms = started.elapsed().as_secs_f64() * 1000.0;
        info!(
            model = %parsed.model.as_deref().unwrap_or(model),
            dim,
            batch = texts.len(),
            latency_ms,
            "models embed completed"
        );

        Ok(EmbedResult {
            model: parsed.model.unwrap_or_else(|| model.to_string()),
            dim,
            embeddings,
        })
    }
}

/// Deterministic fake Models client for unit/integration tests.
pub struct FakeModelsClient {
    pub dim: usize,
    pub fail: std::sync::Mutex<Option<&'static str>>,
    pub calls: std::sync::Mutex<usize>,
    overrides: std::sync::Mutex<std::collections::HashMap<String, Vec<f32>>>,
}

impl FakeModelsClient {
    pub fn new(dim: usize) -> Arc<Self> {
        Arc::new(Self {
            dim,
            fail: std::sync::Mutex::new(None),
            calls: std::sync::Mutex::new(0),
            overrides: std::sync::Mutex::new(std::collections::HashMap::new()),
        })
    }

    pub fn set_unavailable(&self) {
        *self.fail.lock().unwrap() = Some("unavailable");
    }

    pub fn clear_fail(&self) {
        *self.fail.lock().unwrap() = None;
    }

    pub fn set_vector(&self, text: &str, vector: Vec<f32>) {
        self.overrides
            .lock()
            .unwrap()
            .insert(text.to_string(), vector);
    }

    fn vector_for(&self, text: &str) -> Vec<f32> {
        if let Some(v) = self.overrides.lock().unwrap().get(text) {
            return v.clone();
        }
        let mut out = vec![0.0f32; self.dim];
        for (i, b) in text.bytes().enumerate() {
            out[i % self.dim] += (b as f32) / 255.0;
        }
        let norm = out.iter().map(|x| x * x).sum::<f32>().sqrt().max(1e-6);
        for v in &mut out {
            *v /= norm;
        }
        out
    }
}

#[async_trait::async_trait]
impl ModelsClient for FakeModelsClient {
    async fn embed(
        &self,
        model: &str,
        texts: &[String],
        _project_id: Option<&str>,
    ) -> Result<EmbedResult, ModelsClientError> {
        *self.calls.lock().unwrap() += 1;
        if self.fail.lock().unwrap().is_some() {
            return Err(ModelsClientError::Unavailable(
                "embedding backend unavailable: fake down".into(),
            ));
        }
        let embeddings: Vec<Vec<f32>> = texts.iter().map(|t| self.vector_for(t)).collect();
        Ok(EmbedResult {
            model: model.to_string(),
            dim: self.dim,
            embeddings,
        })
    }
}
