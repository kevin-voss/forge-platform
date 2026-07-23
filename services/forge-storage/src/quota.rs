//! Per-project storage quota helpers (13.06).

/// Default project quota when `FORGE_STORAGE_DEFAULT_QUOTA_BYTES` is unset (1 GiB).
pub const DEFAULT_QUOTA_BYTES: u64 = 1_073_741_824;

/// True when committing `incoming_bytes` (replacing `replacing_bytes` for an overwrite)
/// would exceed `quota_bytes`. At-limit (`==`) is accepted; strictly over is rejected.
pub fn would_exceed(used_bytes: i64, incoming_bytes: i64, replacing_bytes: i64, quota_bytes: i64) -> bool {
    let used = used_bytes.max(0);
    let incoming = incoming_bytes.max(0);
    let replacing = replacing_bytes.max(0);
    let quota = quota_bytes.max(0);
    let after = used.saturating_sub(replacing).saturating_add(incoming);
    after > quota
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn at_limit_accepted_over_limit_rejected() {
        // used=90, incoming=10, quota=100 → exactly at limit → accept
        assert!(!would_exceed(90, 10, 0, 100));
        // used=91, incoming=10, quota=100 → 101 → reject
        assert!(would_exceed(91, 10, 0, 100));
        // overwrite credits old size: used=100, replace 40 with 40 → accept
        assert!(!would_exceed(100, 40, 40, 100));
        // overwrite grow past quota: used=100, replace 40 with 50 → reject
        assert!(would_exceed(100, 50, 40, 100));
        // empty project: 0 + 0 <= quota
        assert!(!would_exceed(0, 0, 0, 100));
    }
}
