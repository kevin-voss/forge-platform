//! HMAC-SHA256 signed access tokens with expiry (13.05).

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::Sha256;
use subtle::ConstantTimeEq;
use tracing::warn;

type HmacSha256 = Hmac<Sha256>;

pub const TOKEN_VERSION: u32 = 1;

/// Unix-seconds clock used for issue/verify (injectable for tests).
pub type Clock = std::sync::Arc<dyn Fn() -> i64 + Send + Sync>;

pub fn system_clock() -> Clock {
    std::sync::Arc::new(|| chrono::Utc::now().timestamp())
}

/// Active signing material + TTL/skew policy.
#[derive(Clone)]
pub struct SigningKeys {
    pub key: Vec<u8>,
    pub key_prev: Option<Vec<u8>>,
    pub max_ttl_seconds: u64,
    pub clock_skew_seconds: i64,
}

impl std::fmt::Debug for SigningKeys {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SigningKeys")
            .field("key", &"<redacted>")
            .field("key_prev", &self.key_prev.as_ref().map(|_| "<redacted>"))
            .field("max_ttl_seconds", &self.max_ttl_seconds)
            .field("clock_skew_seconds", &self.clock_skew_seconds)
            .finish()
    }
}

/// Claims carried in the unsigned payload portion of a token.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TokenClaims {
    pub v: u32,
    pub method: String,
    pub project_id: String,
    pub bucket: String,
    pub key: String,
    pub exp: i64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SignError {
    TtlTooLarge { max: u64 },
    InvalidMethod,
    InvalidTtl,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VerifyError {
    /// String is not a forge-storage signed token (fall through to other auth).
    NotOurFormat,
    InvalidToken,
    TokenExpired,
    MethodMismatch,
    ScopeMismatch,
}

impl VerifyError {
    pub fn reason(&self) -> &'static str {
        match self {
            Self::NotOurFormat => "not_our_format",
            Self::InvalidToken => "invalid_token",
            Self::TokenExpired => "token_expired",
            Self::MethodMismatch => "method_mismatch",
            Self::ScopeMismatch => "scope_mismatch",
        }
    }
}

/// Canonical string: method, project_id, bucket, key, expiry (newline-separated).
pub fn canonical_string(
    method: &str,
    project_id: &str,
    bucket: &str,
    key: &str,
    exp: i64,
) -> String {
    format!("{method}\n{project_id}\n{bucket}\n{key}\n{exp}")
}

fn hmac_sign(key: &[u8], message: &[u8]) -> Result<[u8; 32], ()> {
    let mut mac = HmacSha256::new_from_slice(key).map_err(|_| ())?;
    mac.update(message);
    let result = mac.finalize().into_bytes();
    let mut out = [0u8; 32];
    out.copy_from_slice(&result);
    Ok(out)
}

fn hmac_verify(key: &[u8], message: &[u8], expected: &[u8]) -> bool {
    let Ok(actual) = hmac_sign(key, message) else {
        return false;
    };
    if expected.len() != actual.len() {
        return false;
    }
    bool::from(actual.ct_eq(expected))
}

/// Normalize HTTP method for tokens (GET|PUT only).
pub fn normalize_method(raw: &str) -> Option<&'static str> {
    match raw.trim().to_ascii_uppercase().as_str() {
        "GET" => Some("GET"),
        "PUT" => Some("PUT"),
        _ => None,
    }
}

/// Issue a signed token for a scoped object operation.
pub fn issue_token(
    keys: &SigningKeys,
    method: &str,
    project_id: &str,
    bucket: &str,
    key: &str,
    ttl_seconds: u64,
    now: i64,
) -> Result<(String, TokenClaims), SignError> {
    if ttl_seconds == 0 {
        return Err(SignError::InvalidTtl);
    }
    if ttl_seconds > keys.max_ttl_seconds {
        return Err(SignError::TtlTooLarge {
            max: keys.max_ttl_seconds,
        });
    }
    let method = normalize_method(method).ok_or(SignError::InvalidMethod)?;
    let exp = now.saturating_add(ttl_seconds as i64);
    let claims = TokenClaims {
        v: TOKEN_VERSION,
        method: method.to_string(),
        project_id: project_id.to_string(),
        bucket: bucket.to_string(),
        key: key.to_string(),
        exp,
    };
    let payload = serde_json::to_vec(&claims).expect("claims serialize");
    let canonical = canonical_string(method, project_id, bucket, key, exp);
    let sig = hmac_sign(&keys.key, canonical.as_bytes()).expect("HMAC key length");
    let token = format!(
        "{}.{}",
        URL_SAFE_NO_PAD.encode(payload),
        URL_SAFE_NO_PAD.encode(sig)
    );
    Ok((token, claims))
}

/// Decode payload without verifying (format check only).
pub fn decode_claims(token: &str) -> Result<TokenClaims, VerifyError> {
    let (payload_b64, _sig_b64) = split_token(token)?;
    let payload = URL_SAFE_NO_PAD
        .decode(payload_b64.as_bytes())
        .map_err(|_| VerifyError::NotOurFormat)?;
    let claims: TokenClaims =
        serde_json::from_slice(&payload).map_err(|_| VerifyError::NotOurFormat)?;
    if claims.v != TOKEN_VERSION {
        return Err(VerifyError::InvalidToken);
    }
    if normalize_method(&claims.method).is_none() {
        return Err(VerifyError::InvalidToken);
    }
    Ok(claims)
}

fn split_token(token: &str) -> Result<(&str, &str), VerifyError> {
    let token = token.trim();
    if token.is_empty() {
        return Err(VerifyError::NotOurFormat);
    }
    let (payload_b64, sig_b64) = token
        .split_once('.')
        .ok_or(VerifyError::NotOurFormat)?;
    if payload_b64.is_empty() || sig_b64.is_empty() || sig_b64.contains('.') {
        return Err(VerifyError::NotOurFormat);
    }
    Ok((payload_b64, sig_b64))
}

/// Verify signature (current key, then optional previous), expiry+skew, and scope.
pub fn verify_token(
    keys: &SigningKeys,
    token: &str,
    method: &str,
    project_id: Option<&str>,
    bucket: &str,
    key: &str,
    now: i64,
) -> Result<TokenClaims, VerifyError> {
    let (payload_b64, sig_b64) = split_token(token)?;
    let payload = URL_SAFE_NO_PAD
        .decode(payload_b64.as_bytes())
        .map_err(|_| VerifyError::NotOurFormat)?;
    let sig = URL_SAFE_NO_PAD
        .decode(sig_b64.as_bytes())
        .map_err(|_| VerifyError::InvalidToken)?;
    let claims: TokenClaims =
        serde_json::from_slice(&payload).map_err(|_| VerifyError::NotOurFormat)?;
    if claims.v != TOKEN_VERSION {
        return Err(VerifyError::InvalidToken);
    }
    let method_norm = normalize_method(method).ok_or(VerifyError::MethodMismatch)?;
    let claims_method =
        normalize_method(&claims.method).ok_or(VerifyError::InvalidToken)?;

    let canonical = canonical_string(
        claims_method,
        &claims.project_id,
        &claims.bucket,
        &claims.key,
        claims.exp,
    );
    let sig_ok = hmac_verify(&keys.key, canonical.as_bytes(), &sig)
        || keys
            .key_prev
            .as_deref()
            .map(|prev| hmac_verify(prev, canonical.as_bytes(), &sig))
            .unwrap_or(false);
    if !sig_ok {
        // Tampered or wrong key — do not leak which.
        warn!(
            project_id = %claims.project_id,
            bucket = %claims.bucket,
            key = %claims.key,
            method = %claims.method,
            "signed token signature verification failed"
        );
        return Err(VerifyError::InvalidToken);
    }

    // Strict expiry with configurable clock-skew tolerance: valid while now <= exp + skew.
    if now > claims.exp.saturating_add(keys.clock_skew_seconds) {
        return Err(VerifyError::TokenExpired);
    }

    if claims_method != method_norm {
        return Err(VerifyError::MethodMismatch);
    }
    if claims.bucket != bucket || claims.key != key {
        return Err(VerifyError::ScopeMismatch);
    }
    if let Some(pid) = project_id {
        if claims.project_id != pid {
            return Err(VerifyError::ScopeMismatch);
        }
    }

    Ok(claims)
}

/// True when `raw` looks like a forge-storage signed token (payload.v present).
pub fn looks_like_signed_token(raw: &str) -> bool {
    decode_claims(raw).is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn keys() -> SigningKeys {
        SigningKeys {
            key: b"unit-test-signing-key-aaaaaaaa".to_vec(),
            key_prev: None,
            max_ttl_seconds: 3600,
            clock_skew_seconds: 30,
        }
    }

    #[test]
    fn sign_verify_round_trip() {
        let keys = keys();
        let now = 1_700_000_000;
        let (token, claims) =
            issue_token(&keys, "GET", "proj-a", "artifacts", "a.bin", 300, now).unwrap();
        assert_eq!(claims.exp, now + 300);
        let got = verify_token(
            &keys,
            &token,
            "GET",
            Some("proj-a"),
            "artifacts",
            "a.bin",
            now + 10,
        )
        .unwrap();
        assert_eq!(got, claims);
    }

    #[test]
    fn tampered_payload_fails() {
        let keys = keys();
        let now = 1_700_000_000;
        let (token, _) =
            issue_token(&keys, "GET", "proj-a", "artifacts", "a.bin", 300, now).unwrap();
        let (payload, sig) = token.split_once('.').unwrap();
        let mut bytes = URL_SAFE_NO_PAD.decode(payload).unwrap();
        // Flip a byte inside the JSON.
        let idx = bytes.iter().position(|&b| b == b'a').unwrap();
        bytes[idx] = b'b';
        let tampered = format!("{}.{}", URL_SAFE_NO_PAD.encode(bytes), sig);
        let err = verify_token(
            &keys,
            &tampered,
            "GET",
            Some("proj-a"),
            "artifacts",
            "a.bin",
            now,
        )
        .unwrap_err();
        assert_eq!(err, VerifyError::InvalidToken);
    }

    #[test]
    fn tampered_signature_fails() {
        let keys = keys();
        let now = 1_700_000_000;
        let (token, _) =
            issue_token(&keys, "GET", "proj-a", "artifacts", "a.bin", 300, now).unwrap();
        let (payload, sig) = token.split_once('.').unwrap();
        let mut sig_bytes = URL_SAFE_NO_PAD.decode(sig).unwrap();
        sig_bytes[0] ^= 0xff;
        let tampered = format!("{}.{}", payload, URL_SAFE_NO_PAD.encode(sig_bytes));
        assert_eq!(
            verify_token(
                &keys,
                &tampered,
                "GET",
                Some("proj-a"),
                "artifacts",
                "a.bin",
                now
            )
            .unwrap_err(),
            VerifyError::InvalidToken
        );
    }

    #[test]
    fn expiry_boundary_with_skew() {
        let keys = keys();
        let now = 1_700_000_000;
        let (token, claims) =
            issue_token(&keys, "GET", "proj-a", "artifacts", "a.bin", 60, now).unwrap();
        // exp - 1 → valid
        verify_token(
            &keys,
            &token,
            "GET",
            None,
            "artifacts",
            "a.bin",
            claims.exp - 1,
        )
        .unwrap();
        // exp + skew → still valid (boundary inclusive)
        verify_token(
            &keys,
            &token,
            "GET",
            None,
            "artifacts",
            "a.bin",
            claims.exp + keys.clock_skew_seconds,
        )
        .unwrap();
        // exp + skew + 1 → expired
        assert_eq!(
            verify_token(
                &keys,
                &token,
                "GET",
                None,
                "artifacts",
                "a.bin",
                claims.exp + keys.clock_skew_seconds + 1,
            )
            .unwrap_err(),
            VerifyError::TokenExpired
        );
    }

    #[test]
    fn ttl_clamp_rejects_over_max() {
        let keys = keys();
        let err = issue_token(
            &keys,
            "GET",
            "proj-a",
            "artifacts",
            "a.bin",
            keys.max_ttl_seconds + 1,
            0,
        )
        .unwrap_err();
        assert_eq!(
            err,
            SignError::TtlTooLarge {
                max: keys.max_ttl_seconds
            }
        );
    }

    #[test]
    fn method_mismatch_rejected() {
        let keys = keys();
        let (token, _) =
            issue_token(&keys, "GET", "proj-a", "artifacts", "a.bin", 60, 100).unwrap();
        assert_eq!(
            verify_token(&keys, &token, "PUT", None, "artifacts", "a.bin", 100).unwrap_err(),
            VerifyError::MethodMismatch
        );
    }

    #[test]
    fn previous_key_verifies_old_token() {
        let prev = b"previous-signing-key-bbbbbbbbbb".to_vec();
        let mut old = keys();
        old.key = prev.clone();
        let (token, _) =
            issue_token(&old, "GET", "proj-a", "artifacts", "a.bin", 60, 100).unwrap();

        let mut current = keys();
        current.key_prev = Some(prev);
        verify_token(
            &current,
            &token,
            "GET",
            None,
            "artifacts",
            "a.bin",
            100,
        )
        .unwrap();

        // New tokens use current key.
        let (new_token, _) =
            issue_token(&current, "GET", "proj-a", "artifacts", "a.bin", 60, 100).unwrap();
        let mut prev_only = keys();
        prev_only.key = current.key_prev.clone().unwrap();
        prev_only.key_prev = None;
        assert_eq!(
            verify_token(
                &prev_only,
                &new_token,
                "GET",
                None,
                "artifacts",
                "a.bin",
                100
            )
            .unwrap_err(),
            VerifyError::InvalidToken
        );
    }

    #[test]
    fn debug_redacts_keys() {
        let dbg = format!("{:?}", keys());
        assert!(dbg.contains("<redacted>"));
        assert!(!dbg.contains("unit-test-signing-key"));
    }
}
