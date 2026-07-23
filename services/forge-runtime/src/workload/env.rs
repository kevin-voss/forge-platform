//! Sensitive environment injection helpers (epic 10 / step 10.04).
//!
//! Injected secret/config values must never appear in Runtime logs or status
//! responses. Only key names and the secrets fingerprint (hash) are safe.

use std::collections::HashMap;

/// Docker label carrying the secrets/config version fingerprint (hash only).
pub const SECRETS_FINGERPRINT_LABEL: &str = "forge.secrets_fingerprint";

/// Env key also set on the container for app-side verification (hash only).
pub const SECRETS_FINGERPRINT_ENV: &str = "FORGE_SECRETS_FINGERPRINT";

/// Resolve the secrets fingerprint from the create body and/or env map.
pub fn resolve_fingerprint(
    explicit: Option<&str>,
    environment: &HashMap<String, String>,
) -> Option<String> {
    explicit
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(|s| s.to_string())
        .or_else(|| {
            environment
                .get(SECRETS_FINGERPRINT_ENV)
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
        })
}

/// Sorted env key names for safe logging (never values).
pub fn env_keys_for_log(environment: &HashMap<String, String>) -> Vec<String> {
    let mut keys: Vec<String> = environment.keys().cloned().collect();
    keys.sort();
    keys
}

/// True when a log/status payload string appears to contain a secret value.
/// Used in tests to assert masking; production paths must not log values at all.
pub fn payload_leaks_secret_values(payload: &str, environment: &HashMap<String, String>) -> bool {
    environment.values().any(|v| {
        let trimmed = v.trim();
        !trimmed.is_empty() && trimmed.len() >= 3 && payload.contains(trimmed)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fingerprint_from_explicit_or_env() {
        let mut env = HashMap::new();
        env.insert(SECRETS_FINGERPRINT_ENV.into(), "abc123".into());
        assert_eq!(
            resolve_fingerprint(Some("explicit"), &env).as_deref(),
            Some("explicit")
        );
        assert_eq!(resolve_fingerprint(None, &env).as_deref(), Some("abc123"));
        assert_eq!(resolve_fingerprint(Some("  "), &HashMap::new()), None);
    }

    #[test]
    fn env_keys_sorted_no_values() {
        let mut env = HashMap::new();
        env.insert("ZEBRA".into(), "secret-value".into());
        env.insert("ALPHA".into(), "other".into());
        assert_eq!(env_keys_for_log(&env), vec!["ALPHA", "ZEBRA"]);
    }

    #[test]
    fn leak_detector_finds_values() {
        let mut env = HashMap::new();
        env.insert("DATABASE_PASSWORD".into(), "pw1-super-secret".into());
        assert!(payload_leaks_secret_values(
            "oops pw1-super-secret leaked",
            &env
        ));
        assert!(!payload_leaks_secret_values(
            "keys=[DATABASE_PASSWORD] fingerprint=abc",
            &env
        ));
    }
}
