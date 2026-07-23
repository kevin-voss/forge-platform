//! Lightweight Identity introspect client for enforced auth mode.

use serde::Deserialize;
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tracing::warn;

#[derive(Debug, Clone, Deserialize)]
pub struct IntrospectMembershipProject {
    pub project_id: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct IntrospectMemberships {
    #[serde(default)]
    pub projects: Vec<IntrospectMembershipProject>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Principal {
    pub active: bool,
    pub principal_type: Option<String>,
    pub principal_id: Option<String>,
    pub project_id: Option<String>,
    pub memberships: Option<IntrospectMemberships>,
}

impl Principal {
    pub fn allows_project(&self, project_id: &str) -> bool {
        if let Some(pid) = self
            .project_id
            .as_deref()
            .map(str::trim)
            .filter(|s| !s.is_empty())
        {
            return pid == project_id;
        }
        if let Some(m) = &self.memberships {
            return m.projects.iter().any(|p| {
                p.project_id
                    .as_deref()
                    .map(|id| id == project_id)
                    .unwrap_or(false)
            });
        }
        false
    }
}

#[async_trait::async_trait]
pub trait IdentityClient: Send + Sync {
    async fn introspect(&self, token: &str) -> Result<Principal, String>;
}

#[derive(Clone)]
struct CacheEntry {
    principal: Principal,
    expires_at: Instant,
}

/// HTTP Identity client (`POST /v1/auth/introspect`).
pub struct HttpIdentityClient {
    base: String,
    http: reqwest::Client,
    ttl: Duration,
    cache: Mutex<HashMap<String, CacheEntry>>,
}

impl HttpIdentityClient {
    pub fn new(identity_url: &str, cache_ttl_secs: u64) -> Result<Arc<Self>, String> {
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(3))
            .connect_timeout(Duration::from_secs(2))
            .build()
            .map_err(|e| format!("identity http client: {e}"))?;
        Ok(Arc::new(Self {
            base: identity_url.trim_end_matches('/').to_string(),
            http,
            ttl: Duration::from_secs(cache_ttl_secs.max(1)),
            cache: Mutex::new(HashMap::new()),
        }))
    }
}

#[async_trait::async_trait]
impl IdentityClient for HttpIdentityClient {
    async fn introspect(&self, token: &str) -> Result<Principal, String> {
        let token = token.trim();
        if token.is_empty() {
            return Err("empty token".into());
        }
        if let Ok(guard) = self.cache.lock() {
            if let Some(entry) = guard.get(token) {
                if Instant::now() < entry.expires_at {
                    return Ok(entry.principal.clone());
                }
            }
        }

        #[derive(serde::Serialize)]
        struct Body<'a> {
            token: &'a str,
        }

        let url = format!("{}/v1/auth/introspect", self.base);
        let resp = self
            .http
            .post(&url)
            .json(&Body { token })
            .send()
            .await
            .map_err(|e| {
                warn!(error = %e, "identity introspect transport failed");
                format!("introspect transport: {e}")
            })?;
        if !resp.status().is_success() {
            return Err(format!("introspect status {}", resp.status()));
        }
        let principal: Principal = resp
            .json()
            .await
            .map_err(|e| format!("introspect decode: {e}"))?;
        if let Ok(mut guard) = self.cache.lock() {
            guard.insert(
                token.to_string(),
                CacheEntry {
                    principal: principal.clone(),
                    expires_at: Instant::now() + self.ttl,
                },
            );
        }
        Ok(principal)
    }
}

/// Test double that returns a fixed principal for any non-empty token.
#[cfg(test)]
pub struct StubIdentity {
    pub principal: Principal,
}

#[cfg(test)]
#[async_trait::async_trait]
impl IdentityClient for StubIdentity {
    async fn introspect(&self, token: &str) -> Result<Principal, String> {
        if token.is_empty() {
            return Err("empty".into());
        }
        Ok(self.principal.clone())
    }
}
