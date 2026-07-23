use crate::crypto::data_key::NONCE_LEN;

/// XChaCha20-Poly1305 nonce length.
pub const XCHACHA_NONCE_LEN: usize = 24;

/// AEAD algorithm used to encrypt secret values at rest.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AeadAlg {
    Aes256Gcm,
    XChaCha20Poly1305,
}

impl AeadAlg {
    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "" | "aes-256-gcm" | "aes256gcm" => Ok(Self::Aes256Gcm),
            "xchacha20poly1305" | "xchacha20-poly1305" => Ok(Self::XChaCha20Poly1305),
            other => Err(format!(
                "FORGE_SECRETS_AEAD_ALG must be aes-256-gcm|xchacha20poly1305, got {other:?}"
            )),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Aes256Gcm => "aes-256-gcm",
            Self::XChaCha20Poly1305 => "xchacha20poly1305",
        }
    }

    pub fn nonce_len(self) -> usize {
        match self {
            Self::Aes256Gcm => NONCE_LEN,
            Self::XChaCha20Poly1305 => XCHACHA_NONCE_LEN,
        }
    }
}
