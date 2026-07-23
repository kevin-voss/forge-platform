//! Reusable secret-value log masking for Forge platform services.
//!
//! Convention (adopted by forge-secrets; other services import this pattern):
//! 1. Keep known secret values **only in memory** for the lifetime of the process
//!    (or request), never in a secondary plaintext store.
//! 2. Before writing a log line, replace every known value with the placeholder
//!    (default `***`).
//! 3. Prefer not logging secret fields at all; masking is a safety net.
//!
//! See `docs/contracts/secret-log-masking.md`.

use std::collections::HashSet;
use std::io::{self, Write};
use std::sync::{Arc, OnceLock, RwLock};
use tracing_subscriber::fmt::MakeWriter;

/// Default redaction placeholder (`FORGE_MASK_PLACEHOLDER`).
pub const DEFAULT_PLACEHOLDER: &str = "***";

/// In-memory set of secret values that must never appear in logs.
#[derive(Debug, Default)]
pub struct KnownSecrets {
    values: RwLock<HashSet<String>>,
    masked_lines: std::sync::atomic::AtomicU64,
}

impl KnownSecrets {
    pub fn new() -> Self {
        Self::default()
    }

    /// Remember a secret value for masking. Empty / placeholder-like values are ignored.
    pub fn register(&self, value: &str) {
        let trimmed = value.trim();
        if trimmed.is_empty() || trimmed == DEFAULT_PLACEHOLDER || trimmed.len() < 3 {
            return;
        }
        if let Ok(mut guard) = self.values.write() {
            guard.insert(trimmed.to_string());
        }
    }

    pub fn register_many<I, S>(&self, values: I)
    where
        I: IntoIterator<Item = S>,
        S: AsRef<str>,
    {
        for v in values {
            self.register(v.as_ref());
        }
    }

    pub fn snapshot(&self) -> Vec<String> {
        self.values
            .read()
            .map(|g| {
                let mut v: Vec<String> = g.iter().cloned().collect();
                // Longest first so substrings of longer secrets don't leave remnants.
                v.sort_by_key(|b| std::cmp::Reverse(b.len()));
                v
            })
            .unwrap_or_default()
    }

    pub fn masked_lines_total(&self) -> u64 {
        self.masked_lines.load(std::sync::atomic::Ordering::Relaxed)
    }

    fn note_masked_line(&self) {
        self.masked_lines
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }
}

/// Process-wide registry used by the Secrets log layer and route hooks.
pub fn global_known_secrets() -> Arc<KnownSecrets> {
    static CELL: OnceLock<Arc<KnownSecrets>> = OnceLock::new();
    CELL.get_or_init(|| Arc::new(KnownSecrets::new())).clone()
}

/// Replace every occurrence of each known secret value with `placeholder`.
///
/// On internal error, errs toward redaction by returning a fully redacted line.
pub fn mask_text(text: &str, known: &[String], placeholder: &str) -> String {
    if known.is_empty() {
        return text.to_string();
    }
    let mut out = text.to_string();
    let mut changed = false;
    for value in known {
        if value.is_empty() {
            continue;
        }
        if out.contains(value) {
            out = out.replace(value, placeholder);
            changed = true;
        }
    }
    let _ = changed;
    out
}

/// Mask using the process-wide registry.
pub fn mask_with_global(text: &str, placeholder: &str) -> String {
    let known = global_known_secrets().snapshot();
    mask_text(text, &known, placeholder)
}

/// `MakeWriter` that redacts known secret values from every log line written.
#[derive(Clone)]
pub struct MaskingMakeWriter {
    known: Arc<KnownSecrets>,
    placeholder: String,
    enabled: bool,
    inner: fn() -> io::Stdout,
}

impl MaskingMakeWriter {
    pub fn stdout(known: Arc<KnownSecrets>, placeholder: impl Into<String>, enabled: bool) -> Self {
        Self {
            known,
            placeholder: placeholder.into(),
            enabled,
            inner: io::stdout,
        }
    }
}

impl<'a> MakeWriter<'a> for MaskingMakeWriter {
    type Writer = MaskingWriter;

    fn make_writer(&'a self) -> Self::Writer {
        MaskingWriter {
            known: self.known.clone(),
            placeholder: self.placeholder.clone(),
            enabled: self.enabled,
            buf: Vec::new(),
            inner: (self.inner)(),
        }
    }
}

pub struct MaskingWriter {
    known: Arc<KnownSecrets>,
    placeholder: String,
    enabled: bool,
    buf: Vec<u8>,
    inner: io::Stdout,
}

impl Write for MaskingWriter {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.buf.extend_from_slice(buf);
        // Flush complete lines so we can redact whole lines safely.
        while let Some(pos) = self.buf.iter().position(|&b| b == b'\n') {
            let line = self.buf.drain(..=pos).collect::<Vec<u8>>();
            if self.enabled {
                match String::from_utf8(line) {
                    Ok(text) => {
                        let known = self.known.snapshot();
                        let masked = mask_text(&text, &known, &self.placeholder);
                        if masked != text {
                            self.known.note_masked_line();
                        }
                        // Never emit original if masking somehow failed mid-value:
                        // if any known value remains, drop dynamic content.
                        let final_line = if known.iter().any(|v| masked.contains(v.as_str())) {
                            self.known.note_masked_line();
                            "[redacted]\n".to_string()
                        } else {
                            masked
                        };
                        self.inner.write_all(final_line.as_bytes())?;
                    }
                    Err(_) => {
                        // Non-UTF8: drop the line (err toward redaction).
                        self.known.note_masked_line();
                        self.inner.write_all(b"[redacted]\n")?;
                    }
                }
            } else {
                self.inner.write_all(&line)?;
            }
        }
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        if !self.buf.is_empty() {
            let line = std::mem::take(&mut self.buf);
            if self.enabled {
                if let Ok(text) = String::from_utf8(line) {
                    let known = self.known.snapshot();
                    let masked = mask_text(&text, &known, &self.placeholder);
                    if masked != text {
                        self.known.note_masked_line();
                    }
                    self.inner.write_all(masked.as_bytes())?;
                } else {
                    self.known.note_masked_line();
                    self.inner.write_all(b"[redacted]")?;
                }
            } else {
                self.inner.write_all(&line)?;
            }
        }
        self.inner.flush()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn replaces_known_value_mid_string() {
        let known = vec!["pw-secret".into()];
        let out = mask_text(r#"{"msg":"echo attempt pw-secret in log"}"#, &known, "***");
        assert!(out.contains("***"));
        assert!(!out.contains("pw-secret"));
        assert!(out.contains("echo attempt"));
    }

    #[test]
    fn leaves_non_secret_text_intact() {
        let known = vec!["pw-secret".into()];
        let text = r#"{"action":"secret.access","name":"DATABASE_PASSWORD"}"#;
        assert_eq!(mask_text(text, &known, "***"), text);
    }

    #[test]
    fn never_emits_original_when_registered() {
        let ks = KnownSecrets::new();
        ks.register("super-secret-value-99");
        let snap = ks.snapshot();
        let out = mask_text(
            "before super-secret-value-99 after",
            &snap,
            DEFAULT_PLACEHOLDER,
        );
        assert_eq!(out, "before *** after");
        assert!(!out.contains("super-secret-value-99"));
    }

    #[test]
    fn longest_match_first() {
        let mut ordered: Vec<String> = vec!["ab".into(), "abcd".into()];
        ordered.sort_by(|a, b| b.len().cmp(&a.len()));
        let out = mask_text("xxabcdyy", &ordered, "***");
        assert_eq!(out, "xx***yy");
    }
}
