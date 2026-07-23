//! HTTP `Range` / `Content-Range` helpers for byte-range downloads (13.04).

/// Inclusive byte range `[start, end]` against a known object size.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SatisfiedRange {
    pub start: u64,
    pub end: u64,
}

impl SatisfiedRange {
    pub fn len(&self) -> u64 {
        self.end.saturating_sub(self.start).saturating_add(1)
    }

    pub fn content_range_header(&self, total: u64) -> String {
        format!("bytes {}-{}/{}", self.start, self.end, total)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RangeError {
    /// Header present but syntactically invalid or multi-range (unsupported).
    Invalid,
    /// Valid syntax but not satisfiable for `total` (→ 416).
    Unsatisfiable,
}

/// Parse a single `bytes=` Range header against `total` object size.
///
/// Supports `bytes=start-end`, `bytes=start-` (open-ended), and `bytes=-suffix`.
/// Multi-range requests are rejected as invalid (not required by 13.04).
pub fn parse_bytes_range(header: &str, total: u64) -> Result<SatisfiedRange, RangeError> {
    let trimmed = header.trim();
    let Some(spec) = trimmed.strip_prefix("bytes=") else {
        return Err(RangeError::Invalid);
    };
    let spec = spec.trim();
    if spec.is_empty() || spec.contains(',') {
        return Err(RangeError::Invalid);
    }

    if let Some(suffix_raw) = spec.strip_prefix('-') {
        // Suffix form: last N bytes.
        if suffix_raw.is_empty() || suffix_raw.contains('-') {
            return Err(RangeError::Invalid);
        }
        let suffix: u64 = suffix_raw.parse().map_err(|_| RangeError::Invalid)?;
        if suffix == 0 || total == 0 {
            return Err(RangeError::Unsatisfiable);
        }
        let len = suffix.min(total);
        let start = total - len;
        return Ok(SatisfiedRange {
            start,
            end: total - 1,
        });
    }

    let mut parts = spec.splitn(2, '-');
    let start_raw = parts.next().unwrap_or("");
    let end_raw = parts.next();
    if end_raw.is_none() || start_raw.is_empty() {
        return Err(RangeError::Invalid);
    }
    let start: u64 = start_raw.parse().map_err(|_| RangeError::Invalid)?;
    let end_raw = end_raw.unwrap().trim();

    if total == 0 {
        return Err(RangeError::Unsatisfiable);
    }
    if start >= total {
        return Err(RangeError::Unsatisfiable);
    }

    if end_raw.is_empty() {
        // Open-ended: bytes=start-
        return Ok(SatisfiedRange {
            start,
            end: total - 1,
        });
    }

    let end: u64 = end_raw.parse().map_err(|_| RangeError::Invalid)?;
    if end < start {
        return Err(RangeError::Invalid);
    }
    let end = end.min(total - 1);
    Ok(SatisfiedRange { start, end })
}

/// `Content-Range` value for an unsatisfiable range (`416`).
pub fn unsatisfiable_content_range(total: u64) -> String {
    format!("bytes */{total}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_bounded_range() {
        let r = parse_bytes_range("bytes=0-1023", 5000).unwrap();
        assert_eq!(r, SatisfiedRange { start: 0, end: 1023 });
        assert_eq!(r.len(), 1024);
        assert_eq!(r.content_range_header(5000), "bytes 0-1023/5000");
    }

    #[test]
    fn open_ended_range() {
        let r = parse_bytes_range("bytes=100-", 250).unwrap();
        assert_eq!(r, SatisfiedRange { start: 100, end: 249 });
        assert_eq!(r.len(), 150);
    }

    #[test]
    fn suffix_range() {
        let r = parse_bytes_range("bytes=-100", 250).unwrap();
        assert_eq!(r, SatisfiedRange { start: 150, end: 249 });
    }

    #[test]
    fn clamps_end_to_total() {
        let r = parse_bytes_range("bytes=0-9999", 100).unwrap();
        assert_eq!(r, SatisfiedRange { start: 0, end: 99 });
    }

    #[test]
    fn invalid_and_out_of_range() {
        assert_eq!(
            parse_bytes_range("bytes=100-199", 50),
            Err(RangeError::Unsatisfiable)
        );
        assert_eq!(
            parse_bytes_range("bytes=50-40", 100),
            Err(RangeError::Invalid)
        );
        assert_eq!(
            parse_bytes_range("bytes=0-1,2-3", 100),
            Err(RangeError::Invalid)
        );
        assert_eq!(parse_bytes_range("items=0-1", 100), Err(RangeError::Invalid));
        assert_eq!(
            parse_bytes_range("bytes=-0", 100),
            Err(RangeError::Unsatisfiable)
        );
    }
}
