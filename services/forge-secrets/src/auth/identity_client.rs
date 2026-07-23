//! Identity introspect + authz/check client with short-TTL caches.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tracing::warn;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IntrospectMembershipProject {
    pub project_id: Option<String>,
    pub role: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct IntrospectMemberships {
    #[serde(default)]
    pub projects: Vec<IntrospectMembershipProject>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IntrospectResult {
    pub active: bool,
    pub principal_type: Option<String>,
    pub principal_id: Option<String>,
    pub user_id: Option<String>,
    pub project_id: Option<String>,
    pub role: Option<String>,
    pub memberships: Option<IntrospectMemberships>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuthzDecision {
    pub allow: bool,
    pub role: String,
    pub reason: String,
}

#[derive(Debug)]
pub struct IdentityUnreachable {
    pub message: String,
}

impl std::fmt::Display for IdentityUnreachable {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}

impl std::error::Error for IdentityUnreachable {}

pub trait IdentityClient: Send + Sync {
    fn introspect(
        &self,
        token: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<IntrospectResult, IdentityUnreachable>>
                + Send
                + '_,
        >,
    >;

    fn check_authz(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<AuthzDecision, IdentityUnreachable>>
                + Send
                + '_,
        >,
    >;

    /// Optional cache peek for config.read when Identity is down.
    fn cached_authz_decision(
        &self,
        _principal_type: &str,
        _principal_id: &str,
        _project_id: &str,
        _action: &str,
    ) -> Option<AuthzDecision> {
        None
    }

    fn cached_introspect_result(&self, _token: &str) -> Option<IntrospectResult> {
        None
    }
}

#[derive(Clone)]
struct CacheEntry<T> {
    value: T,
    expires_at: Instant,
}

/// HTTP Identity client (introspect + authz/check) with short-TTL caches.
pub struct HttpIdentityClient {
    base: String,
    http: reqwest::Client,
    introspect_ttl: Duration,
    authz_ttl: Duration,
    introspect_cache: Mutex<HashMap<String, CacheEntry<IntrospectResult>>>,
    authz_cache: Mutex<HashMap<String, CacheEntry<AuthzDecision>>>,
}

impl HttpIdentityClient {
    pub fn new(identity_url: &str, cache_ttl_secs: u64) -> Result<Self, String> {
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(3))
            .connect_timeout(Duration::from_secs(2))
            .build()
            .map_err(|e| format!("identity http client: {e}"))?;
        let ttl = Duration::from_secs(cache_ttl_secs.max(1));
        Ok(Self {
            base: identity_url.trim_end_matches('/').to_string(),
            http,
            introspect_ttl: ttl,
            authz_ttl: ttl,
            introspect_cache: Mutex::new(HashMap::new()),
            authz_cache: Mutex::new(HashMap::new()),
        })
    }

    pub fn into_arc(self) -> Arc<dyn IdentityClient> {
        Arc::new(self)
    }

    fn authz_key(
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> String {
        format!("{principal_type}|{principal_id}|{project_id}|{action}")
    }

    fn get_cached_introspect(&self, token: &str) -> Option<IntrospectResult> {
        let mut guard = self.introspect_cache.lock().ok()?;
        let entry = guard.get(token)?;
        if entry.expires_at <= Instant::now() {
            guard.remove(token);
            return None;
        }
        Some(entry.value.clone())
    }

    fn put_introspect(&self, token: &str, value: IntrospectResult) {
        if let Ok(mut guard) = self.introspect_cache.lock() {
            guard.insert(
                token.to_string(),
                CacheEntry {
                    value,
                    expires_at: Instant::now() + self.introspect_ttl,
                },
            );
        }
    }

    fn get_cached_authz(&self, key: &str) -> Option<AuthzDecision> {
        let mut guard = self.authz_cache.lock().ok()?;
        let entry = guard.get(key)?;
        if entry.expires_at <= Instant::now() {
            guard.remove(key);
            return None;
        }
        Some(entry.value.clone())
    }

    fn put_authz(&self, key: String, value: AuthzDecision) {
        if let Ok(mut guard) = self.authz_cache.lock() {
            guard.insert(
                key,
                CacheEntry {
                    value,
                    expires_at: Instant::now() + self.authz_ttl,
                },
            );
        }
    }
}

impl IdentityClient for HttpIdentityClient {
    fn introspect(
        &self,
        token: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<IntrospectResult, IdentityUnreachable>>
                + Send
                + '_,
        >,
    > {
        let token = token.to_string();
        Box::pin(async move {
            if let Some(cached) = self.get_cached_introspect(&token) {
                return Ok(cached);
            }
            let url = format!("{}/v1/auth/introspect", self.base);
            let body = serde_json::json!({ "token": token });
            let response = self
                .http
                .post(&url)
                .header("content-type", "application/json")
                .header("accept", "application/json")
                .json(&body)
                .send()
                .await
                .map_err(|e| IdentityUnreachable {
                    message: format!("identity unreachable: {e}"),
                })?;
            if !response.status().is_success() {
                let status = response.status();
                let text = response.text().await.unwrap_or_default();
                return Err(IdentityUnreachable {
                    message: format!(
                        "identity HTTP {status}: {}",
                        text.chars().take(200).collect::<String>()
                    ),
                });
            }
            let result: IntrospectResult =
                response.json().await.map_err(|e| IdentityUnreachable {
                    message: format!("identity introspect decode failed: {e}"),
                })?;
            self.put_introspect(&token, result.clone());
            Ok(result)
        })
    }

    fn check_authz(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<AuthzDecision, IdentityUnreachable>>
                + Send
                + '_,
        >,
    > {
        let principal_type = principal_type.to_string();
        let principal_id = principal_id.to_string();
        let project_id = project_id.to_string();
        let action = action.to_string();
        Box::pin(async move {
            let key = Self::authz_key(&principal_type, &principal_id, &project_id, &action);
            if let Some(cached) = self.get_cached_authz(&key) {
                return Ok(cached);
            }
            let url = format!("{}/v1/authz/check", self.base);
            let body = serde_json::json!({
                "principal": { "type": principal_type, "id": principal_id },
                "project_id": project_id,
                "action": action,
            });
            let response = self
                .http
                .post(&url)
                .header("content-type", "application/json")
                .header("accept", "application/json")
                .json(&body)
                .send()
                .await
                .map_err(|e| IdentityUnreachable {
                    message: format!("identity unreachable: {e}"),
                })?;
            if !response.status().is_success() {
                let status = response.status();
                let text = response.text().await.unwrap_or_default();
                return Err(IdentityUnreachable {
                    message: format!(
                        "identity HTTP {status}: {}",
                        text.chars().take(200).collect::<String>()
                    ),
                });
            }
            let decision: AuthzDecision =
                response.json().await.map_err(|e| IdentityUnreachable {
                    message: format!("identity authz decode failed: {e}"),
                })?;
            self.put_authz(key, decision.clone());
            Ok(decision)
        })
    }

    fn cached_authz_decision(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> Option<AuthzDecision> {
        self.get_cached_authz(&Self::authz_key(
            principal_type,
            principal_id,
            project_id,
            action,
        ))
    }

    fn cached_introspect_result(&self, token: &str) -> Option<IntrospectResult> {
        self.get_cached_introspect(token)
    }
}

/// Deterministic fake for unit / isolation tests.
#[derive(Default)]
pub struct FakeIdentityClient {
    introspect_by_token: Mutex<HashMap<String, IntrospectResult>>,
    decisions: Mutex<HashMap<String, AuthzDecision>>,
    /// Pre-seeded "cache" used when unreachable for config.read.
    cache_decisions: Mutex<HashMap<String, AuthzDecision>>,
    cache_introspect: Mutex<HashMap<String, IntrospectResult>>,
    pub unreachable: Mutex<bool>,
    pub introspect_calls: Mutex<u32>,
    pub authz_calls: Mutex<u32>,
}

impl FakeIdentityClient {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn into_arc(self) -> Arc<dyn IdentityClient> {
        Arc::new(self)
    }

    pub fn stub_introspect(&self, token: impl Into<String>, result: IntrospectResult) {
        self.introspect_by_token
            .lock()
            .unwrap()
            .insert(token.into(), result);
    }

    pub fn stub_authz(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
        decision: AuthzDecision,
    ) {
        let key = format!("{principal_type}|{principal_id}|{project_id}|{action}");
        self.decisions.lock().unwrap().insert(key, decision);
    }

    /// Seed cache entries that remain available when Identity is marked unreachable.
    pub fn seed_introspect_cache(&self, token: &str, introspect: IntrospectResult) {
        self.cache_introspect
            .lock()
            .unwrap()
            .insert(token.to_string(), introspect);
    }

    pub fn seed_authz_cache(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
        decision: AuthzDecision,
    ) {
        let key = format!("{principal_type}|{principal_id}|{project_id}|{action}");
        self.cache_decisions.lock().unwrap().insert(key, decision);
    }

    pub fn set_unreachable(&self, v: bool) {
        *self.unreachable.lock().unwrap() = v;
    }
}

impl IdentityClient for FakeIdentityClient {
    fn introspect(
        &self,
        token: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<IntrospectResult, IdentityUnreachable>>
                + Send
                + '_,
        >,
    > {
        let token = token.to_string();
        Box::pin(async move {
            if *self.unreachable.lock().unwrap() {
                return Err(IdentityUnreachable {
                    message: "identity unreachable".into(),
                });
            }
            *self.introspect_calls.lock().unwrap() += 1;
            Ok(self
                .introspect_by_token
                .lock()
                .unwrap()
                .get(&token)
                .cloned()
                .unwrap_or(IntrospectResult {
                    active: false,
                    principal_type: None,
                    principal_id: None,
                    user_id: None,
                    project_id: None,
                    role: None,
                    memberships: None,
                }))
        })
    }

    fn check_authz(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> std::pin::Pin<
        Box<
            dyn std::future::Future<Output = Result<AuthzDecision, IdentityUnreachable>>
                + Send
                + '_,
        >,
    > {
        let principal_type = principal_type.to_string();
        let principal_id = principal_id.to_string();
        let project_id = project_id.to_string();
        let action = action.to_string();
        Box::pin(async move {
            if *self.unreachable.lock().unwrap() {
                return Err(IdentityUnreachable {
                    message: "identity unreachable".into(),
                });
            }
            *self.authz_calls.lock().unwrap() += 1;
            let key = format!("{principal_type}|{principal_id}|{project_id}|{action}");
            Ok(self
                .decisions
                .lock()
                .unwrap()
                .get(&key)
                .cloned()
                .unwrap_or_else(|| {
                    warn!(%key, "fake identity: no stub; denying");
                    AuthzDecision {
                        allow: false,
                        role: "none".into(),
                        reason: "no stub".into(),
                    }
                }))
        })
    }

    fn cached_authz_decision(
        &self,
        principal_type: &str,
        principal_id: &str,
        project_id: &str,
        action: &str,
    ) -> Option<AuthzDecision> {
        let key = format!("{principal_type}|{principal_id}|{project_id}|{action}");
        self.cache_decisions.lock().ok()?.get(&key).cloned()
    }

    fn cached_introspect_result(&self, token: &str) -> Option<IntrospectResult> {
        self.cache_introspect.lock().ok()?.get(token).cloned()
    }
}
