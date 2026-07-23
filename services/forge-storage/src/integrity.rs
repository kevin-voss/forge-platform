//! SHA-256 integrity helpers and content-addressed path derivation (13.04).

use sha2::{Digest, Sha256};

/// Hex-encode a SHA-256 digest (lowercase).
pub fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    hex::encode(digest)
}

/// Content-addressed relative path: `objects/<aa>/<full-hash>` → relative `<aa>/<full-hash>`.
pub fn content_addressed_path(sha256_hex: &str) -> Result<String, String> {
    let hash = sha256_hex.trim().to_ascii_lowercase();
    if hash.len() != 64 || !hash.chars().all(|c| c.is_ascii_hexdigit()) {
        return Err("sha256 must be 64 lowercase hex characters".into());
    }
    Ok(format!("{}/{}", &hash[..2], hash))
}

/// True when `candidate` looks like a SHA-256 hex digest (case-insensitive).
pub fn is_sha256_hex(candidate: &str) -> bool {
    let t = candidate.trim();
    t.len() == 64 && t.chars().all(|c| c.is_ascii_hexdigit())
}

/// Normalize a client-supplied SHA-256 to lowercase hex, or `None` if invalid.
pub fn normalize_sha256(candidate: &str) -> Option<String> {
    let t = candidate.trim();
    if is_sha256_hex(t) {
        Some(t.to_ascii_lowercase())
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sha256_matches_known_vector() {
        // echo -n "hello" | shasum -a 256
        assert_eq!(
            sha256_hex(b"hello"),
            "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
        );
        assert_eq!(
            sha256_hex(b""),
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
    }

    #[test]
    fn content_addressed_path_derivation() {
        let hash = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
        assert_eq!(
            content_addressed_path(hash).unwrap(),
            "2c/2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
        );
        assert!(content_addressed_path("abc").is_err());
        assert!(content_addressed_path("").is_err());
    }

    #[test]
    fn normalize_accepts_uppercase() {
        let upper = "2CF24DBA5FB0A30E26E83B2AC5B9E29E1B161E5C1FA7425E73043362938B9824";
        assert_eq!(
            normalize_sha256(upper).as_deref(),
            Some("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
        );
        assert!(normalize_sha256("not-a-hash").is_none());
    }
}
