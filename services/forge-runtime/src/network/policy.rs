//! NetworkPolicy enforcement (22.05).
//!
//! Runtime polls `GET /v1/nodes/{id}/network-policy-rules` and applies the rule
//! set via an atomic nftables table replace (`forge-policy`). Fetch failures keep
//! the last-known-good rules (fail closed). Denied connections increment a
//! counter, are sampled into detail logs, reported to forge-network, and emit a
//! `network.policy.denied` platform event when events URL is configured.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::time::sleep;
use tracing::{debug, info, warn};
use uuid::Uuid;

/// One compiled rule from forge-network.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
pub struct PolicyRule {
    pub workload_id: String,
    pub direction: String,
    #[serde(default)]
    pub from_cidr: Option<String>,
    #[serde(default)]
    pub to_cidr: Option<String>,
    #[serde(default)]
    pub port: Option<u16>,
    #[serde(default)]
    pub protocol: Option<String>,
    pub action: String,
    #[serde(default)]
    pub reason: Option<String>,
}

/// Per-node rule set response.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
pub struct PolicyRuleSet {
    pub node_id: String,
    pub generation: i64,
    pub rules: Vec<PolicyRule>,
}

/// Applies policy rules (nftables on host; fake in CI).
pub trait PolicyBackend: Send + Sync {
    fn apply(&self, rules: &PolicyRuleSet) -> Result<(), String>;
    #[allow(dead_code)]
    fn generation(&self) -> Option<i64>;
}

/// In-memory backend for unit tests / CI.
#[derive(Debug, Default)]
pub struct FakePolicyBackend {
    state: Mutex<FakePolicyState>,
}

#[derive(Debug, Default)]
struct FakePolicyState {
    generation: Option<i64>,
    rules: Vec<PolicyRule>,
    apply_failures: u32,
}

impl FakePolicyBackend {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(FakePolicyState::default()),
        }
    }

    #[allow(dead_code)]
    pub fn force_fail_next(&self, n: u32) {
        self.state.lock().unwrap().apply_failures = n;
    }

    pub fn rule_count(&self) -> usize {
        self.state.lock().unwrap().rules.len()
    }

    pub fn applied_generation(&self) -> Option<i64> {
        self.state.lock().unwrap().generation
    }
}

impl PolicyBackend for FakePolicyBackend {
    fn apply(&self, rules: &PolicyRuleSet) -> Result<(), String> {
        let mut st = self.state.lock().unwrap();
        if st.apply_failures > 0 {
            st.apply_failures -= 1;
            return Err("forced apply failure".into());
        }
        st.generation = Some(rules.generation);
        st.rules = rules.rules.clone();
        Ok(())
    }

    fn generation(&self) -> Option<i64> {
        self.state.lock().unwrap().generation
    }
}

/// Host nftables backend: atomic table replace for `inet forge-policy`.
pub struct NftablesPolicyBackend {
    last: Mutex<Option<i64>>,
}

impl NftablesPolicyBackend {
    pub fn new() -> Self {
        Self {
            last: Mutex::new(None),
        }
    }
}

impl Default for NftablesPolicyBackend {
    fn default() -> Self {
        Self::new()
    }
}

impl PolicyBackend for NftablesPolicyBackend {
    fn apply(&self, rules: &PolicyRuleSet) -> Result<(), String> {
        let script = render_nftables(rules);
        let out = Command::new("nft")
            .args(["-f", "-"])
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .spawn()
            .and_then(|mut child| {
                use std::io::Write;
                if let Some(stdin) = child.stdin.as_mut() {
                    stdin.write_all(script.as_bytes())?;
                }
                child.wait_with_output()
            })
            .map_err(|e| format!("nft spawn: {e}"))?;
        if !out.status.success() {
            return Err(format!(
                "nft apply failed: {}",
                String::from_utf8_lossy(&out.stderr)
            ));
        }
        *self.last.lock().unwrap() = Some(rules.generation);
        info!(
            generation = rules.generation,
            rules = rules.rules.len(),
            "applied network policy rules via nftables"
        );
        Ok(())
    }

    fn generation(&self) -> Option<i64> {
        *self.last.lock().unwrap()
    }
}

fn render_nftables(rules: &PolicyRuleSet) -> String {
    // Atomic replace: delete + add table in one transaction.
    let mut s = String::from("flush table inet forge-policy\n");
    s.push_str("table inet forge-policy {\n");
    s.push_str("  chain input {\n    type filter hook input priority 0; policy accept;\n");
    for r in &rules.rules {
        if r.direction != "ingress" {
            continue;
        }
        s.push_str(&format!("    # {} {}\n", r.workload_id, r.action));
        if let Some(cidr) = &r.from_cidr {
            let verdict = if r.action == "allow" { "accept" } else { "drop" };
            if let (Some(port), Some(proto)) = (r.port, r.protocol.as_deref()) {
                s.push_str(&format!(
                    "    ip saddr {cidr} {proto} dport {port} counter {verdict}\n"
                ));
            } else {
                s.push_str(&format!("    ip saddr {cidr} counter {verdict}\n"));
            }
        }
    }
    s.push_str("  }\n  chain output {\n    type filter hook output priority 0; policy accept;\n");
    for r in &rules.rules {
        if r.direction != "egress" {
            continue;
        }
        s.push_str(&format!("    # {} {}\n", r.workload_id, r.action));
        if let Some(cidr) = &r.to_cidr {
            let verdict = if r.action == "allow" { "accept" } else { "drop" };
            if let (Some(port), Some(proto)) = (r.port, r.protocol.as_deref()) {
                s.push_str(&format!(
                    "    ip daddr {cidr} {proto} dport {port} counter {verdict}\n"
                ));
            } else {
                s.push_str(&format!("    ip daddr {cidr} counter {verdict}\n"));
            }
        }
    }
    s.push_str("  }\n}\n");
    // Prefer add-or-replace: create empty table first if missing, then flush+reload.
    format!("add table inet forge-policy\n{s}")
}

/// Select backend: fake when `FORGE_NETWORK_POLICY_BACKEND=fake`, else nftables.
pub fn select_policy_backend(raw: &str) -> Arc<dyn PolicyBackend> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "fake" => Arc::new(FakePolicyBackend::new()),
        _ => Arc::new(NftablesPolicyBackend::new()),
    }
}

/// Observability counters for denies / applied generation.
#[derive(Debug, Default)]
pub struct PolicyObs {
    pub denied_total: AtomicU64,
    pub rules_generation: AtomicU64,
    deny_labels: Mutex<HashMap<String, u64>>,
}

impl PolicyObs {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn record_deny(&self, from: &str, to: &str, port: u16) {
        self.denied_total.fetch_add(1, Ordering::Relaxed);
        let key = format!("{from}|{to}|{port}");
        let mut m = self.deny_labels.lock().unwrap();
        *m.entry(key).or_insert(0) += 1;
    }

    pub fn set_generation(&self, gen: i64) {
        if gen >= 0 {
            self.rules_generation
                .store(gen as u64, Ordering::Relaxed);
        }
    }

    pub fn denied_total(&self) -> u64 {
        self.denied_total.load(Ordering::Relaxed)
    }
}

/// Config for the policy poll loop.
#[derive(Clone)]
pub struct PolicyPollConfig {
    pub network_url: String,
    pub node_id: String,
    pub poll_interval: Duration,
    pub backend: Arc<dyn PolicyBackend>,
    pub obs: Arc<PolicyObs>,
    pub deny_sample_rate: f64,
    pub events_url: Option<String>,
}

impl super::NetworkClient {
    pub async fn fetch_policy_rules(&self, node_id: &str) -> Result<PolicyRuleSet, String> {
        let url = format!(
            "{}/v1/nodes/{}/network-policy-rules",
            self.base_url,
            super::enc(node_id)
        );
        let resp = self
            .http
            .get(&url)
            .send()
            .await
            .map_err(|e| format!("fetch policy rules: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("fetch policy rules HTTP {status}: {body}"));
        }
        resp.json::<PolicyRuleSet>()
            .await
            .map_err(|e| format!("decode policy rules: {e}"))
    }

    pub async fn report_policy_denied(
        &self,
        node_id: &str,
        from: &str,
        to: &str,
        port: u16,
        reason: Option<&str>,
    ) -> Result<(), String> {
        #[derive(Serialize)]
        struct Body<'a> {
            from_workload: &'a str,
            to_workload: &'a str,
            port: u16,
            protocol: &'a str,
            #[serde(skip_serializing_if = "Option::is_none")]
            reason: Option<&'a str>,
        }
        let url = format!(
            "{}/v1/nodes/{}/network-policy-denied",
            self.base_url,
            super::enc(node_id)
        );
        let resp = self
            .http
            .post(&url)
            .json(&Body {
                from_workload: from,
                to_workload: to,
                port,
                protocol: "tcp",
                reason,
            })
            .send()
            .await
            .map_err(|e| format!("report deny: {e}"))?;
        if !resp.status().is_success() {
            let status = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(format!("report deny HTTP {status}: {body}"));
        }
        Ok(())
    }
}

/// Record a deny at the enforcement point (metric + sampled log + event + report).
pub async fn record_deny(
    client: &super::NetworkClient,
    cfg: &PolicyPollConfig,
    from_workload: &str,
    to_workload: &str,
    port: u16,
    reason: Option<&str>,
) {
    cfg.obs.record_deny(from_workload, to_workload, port);
    let sample = cfg.deny_sample_rate >= 1.0
        || (cfg.deny_sample_rate > 0.0 && fastrand_f64() < cfg.deny_sample_rate);
    if sample {
        info!(
            event = "network.policy.denied",
            from_workload,
            to_workload,
            port,
            reason = reason.unwrap_or(""),
            "connection denied by NetworkPolicy"
        );
        if let Some(events_url) = cfg.events_url.as_deref() {
            let _ = emit_deny_event(events_url, from_workload, to_workload, port, reason).await;
        }
    }
    if let Err(err) = client
        .report_policy_denied(&cfg.node_id, from_workload, to_workload, port, reason)
        .await
    {
        warn!(error = %err, "failed to report policy deny to forge-network");
    }
}

async fn emit_deny_event(
    events_url: &str,
    from: &str,
    to: &str,
    port: u16,
    reason: Option<&str>,
) -> Result<(), String> {
    let base = events_url.trim().trim_end_matches('/');
    let url = format!("{base}/v1/events");
    let event_id = Uuid::new_v4().to_string();
    let body = serde_json::json!({
        "subject": "network.policy.denied",
        "event_id": event_id,
        "source": "forge-runtime",
        "data": {
            "from_workload": from,
            "to_workload": to,
            "port": port,
            "reason": reason.unwrap_or(""),
            "occurred_at": chrono::Utc::now().to_rfc3339(),
            "schema_version": 1
        }
    });
    let http = reqwest::Client::builder()
        .timeout(Duration::from_secs(5))
        .build()
        .map_err(|e| e.to_string())?;
    let resp = http
        .post(&url)
        .header("Idempotency-Key", &event_id)
        .json(&body)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    if !resp.status().is_success() {
        return Err(format!("events HTTP {}", resp.status().as_u16()));
    }
    Ok(())
}

fn fastrand_f64() -> f64 {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};
    use std::time::{SystemTime, UNIX_EPOCH};
    let mut h = DefaultHasher::new();
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos()
        .hash(&mut h);
    (h.finish() as f64) / (u64::MAX as f64)
}

/// One poll cycle: fetch → apply on generation change; keep last-known-good on failure.
pub async fn policy_poll_once(
    client: &super::NetworkClient,
    cfg: &PolicyPollConfig,
    last: &mut i64,
) {
    match client.fetch_policy_rules(&cfg.node_id).await {
        Ok(rules) => {
            if rules.generation == *last {
                debug!(generation = rules.generation, "policy rules unchanged");
                return;
            }
            match cfg.backend.apply(&rules) {
                Ok(()) => {
                    *last = rules.generation;
                    cfg.obs.set_generation(rules.generation);
                    info!(
                        generation = rules.generation,
                        rules = rules.rules.len(),
                        "applied network policy rule set"
                    );
                }
                Err(err) => {
                    // Fail closed: keep previous rule set; surface Degraded.
                    warn!(
                        error = %err,
                        generation = rules.generation,
                        last_good = *last,
                        "policy apply failed; keeping last-known-good rules"
                    );
                }
            }
        }
        Err(err) => {
            // Fail closed: do not clear rules on fetch failure.
            warn!(
                error = %err,
                last_good = *last,
                "policy rule fetch failed; keeping last-known-good rules"
            );
        }
    }
}

/// Spawn the policy poll loop.
pub fn spawn_policy_poll_loop(cfg: PolicyPollConfig) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let client = match super::NetworkClient::new(&cfg.network_url) {
            Ok(c) => c,
            Err(err) => {
                warn!(error = %err, "policy poll loop disabled");
                return;
            }
        };
        let mut last = -1_i64;
        loop {
            policy_poll_once(&client, &cfg, &mut last).await;
            sleep(cfg.poll_interval).await;
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::network::NetworkClient;
    use httpmock::prelude::*;

    #[tokio::test]
    async fn poll_applies_on_generation_change() {
        let server = MockServer::start();
        let _m = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/nodes/node-a/network-policy-rules");
            then.status(200).json_body(serde_json::json!({
                "node_id": "node-a",
                "generation": 7,
                "rules": [{
                    "workload_id": "wl_1",
                    "direction": "ingress",
                    "from_cidr": "10.100.2.5/32",
                    "port": 8080,
                    "protocol": "tcp",
                    "action": "allow"
                }]
            }));
        });
        let backend = Arc::new(FakePolicyBackend::new());
        let cfg = PolicyPollConfig {
            network_url: server.base_url(),
            node_id: "node-a".into(),
            poll_interval: Duration::from_secs(5),
            backend: backend.clone(),
            obs: Arc::new(PolicyObs::new()),
            deny_sample_rate: 1.0,
            events_url: None,
        };
        let client = NetworkClient::new(&cfg.network_url).unwrap();
        let mut last = -1;
        policy_poll_once(&client, &cfg, &mut last).await;
        assert_eq!(last, 7);
        assert_eq!(backend.rule_count(), 1);
        assert_eq!(backend.applied_generation(), Some(7));
    }

    #[tokio::test]
    async fn fetch_failure_keeps_last_known_good() {
        let server = MockServer::start();
        let mock = server.mock(|when, then| {
            when.method(GET)
                .path("/v1/nodes/node-a/network-policy-rules");
            then.status(503).body("unavailable");
        });
        let backend = Arc::new(FakePolicyBackend::new());
        // Pre-seed last-known-good.
        backend
            .apply(&PolicyRuleSet {
                node_id: "node-a".into(),
                generation: 3,
                rules: vec![PolicyRule {
                    workload_id: "wl_1".into(),
                    direction: "ingress".into(),
                    from_cidr: Some("0.0.0.0/0".into()),
                    to_cidr: None,
                    port: None,
                    protocol: None,
                    action: "deny".into(),
                    reason: Some("default-deny-environment".into()),
                }],
            })
            .unwrap();
        let cfg = PolicyPollConfig {
            network_url: server.base_url(),
            node_id: "node-a".into(),
            poll_interval: Duration::from_secs(5),
            backend: backend.clone(),
            obs: Arc::new(PolicyObs::new()),
            deny_sample_rate: 1.0,
            events_url: None,
        };
        let client = NetworkClient::new(&cfg.network_url).unwrap();
        let mut last = 3;
        policy_poll_once(&client, &cfg, &mut last).await;
        assert_eq!(last, 3);
        assert_eq!(backend.applied_generation(), Some(3));
        assert_eq!(backend.rule_count(), 1);
        mock.assert();
    }

    #[tokio::test]
    async fn record_deny_increments_metric_and_emits_event() {
        let server = MockServer::start();
        let report = server.mock(|when, then| {
            when.method(POST)
                .path("/v1/nodes/node-a/network-policy-denied");
            then.status(202).json_body(serde_json::json!({
                "status": "recorded",
                "event": "network.policy.denied"
            }));
        });
        let events = server.mock(|when, then| {
            when.method(POST).path("/v1/events");
            then.status(202).json_body(serde_json::json!({"ok": true}));
        });
        let obs = Arc::new(PolicyObs::new());
        let cfg = PolicyPollConfig {
            network_url: server.base_url(),
            node_id: "node-a".into(),
            poll_interval: Duration::from_secs(5),
            backend: Arc::new(FakePolicyBackend::new()),
            obs: obs.clone(),
            deny_sample_rate: 1.0,
            events_url: Some(server.base_url()),
        };
        let client = NetworkClient::new(&cfg.network_url).unwrap();
        record_deny(&client, &cfg, "wl_x", "wl_api", 8080, Some("policy-default-deny")).await;
        assert_eq!(obs.denied_total(), 1);
        report.assert();
        events.assert();
    }

    #[test]
    fn nftables_script_contains_atomic_table() {
        let rs = PolicyRuleSet {
            node_id: "n".into(),
            generation: 1,
            rules: vec![PolicyRule {
                workload_id: "wl".into(),
                direction: "ingress".into(),
                from_cidr: Some("10.0.0.1/32".into()),
                to_cidr: None,
                port: Some(8080),
                protocol: Some("tcp".into()),
                action: "deny".into(),
                reason: None,
            }],
        };
        let script = render_nftables(&rs);
        assert!(script.contains("table inet forge-policy"));
        assert!(script.contains("flush table inet forge-policy"));
        assert!(script.contains("drop"));
    }
}
