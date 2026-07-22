use crate::crypto::key_provider::MASTER_KEY_LEN;
use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use rand::RngCore;
use tracing::instrument;

/// Length of a per-project data key (AES-256).
pub const DATA_KEY_LEN: usize = 32;

/// AES-GCM nonce length. Nonces are random per wrap and prepended to ciphertext.
/// Callers must never reuse a (master_key, nonce) pair; random 96-bit nonces are used.
pub const NONCE_LEN: usize = 12;

/// Opaque 32-byte data key used to encrypt secret values (10.02+).
#[derive(Clone, PartialEq, Eq)]
pub struct DataKey([u8; DATA_KEY_LEN]);

impl DataKey {
    pub fn as_bytes(&self) -> &[u8; DATA_KEY_LEN] {
        &self.0
    }
}

impl std::fmt::Debug for DataKey {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("DataKey([redacted])")
    }
}

/// Generate a cryptographically random data key.
pub fn generate_data_key() -> DataKey {
    let mut bytes = [0u8; DATA_KEY_LEN];
    rand::thread_rng().fill_bytes(&mut bytes);
    DataKey(bytes)
}

/// Wrap (encrypt) a data key with the master key using AES-256-GCM.
///
/// Wire format: `nonce (12) || ciphertext+tag`.
#[instrument(name = "wrap_data_key", skip_all)]
pub fn wrap_data_key(
    master_key: &[u8; MASTER_KEY_LEN],
    data_key: &DataKey,
) -> Result<Vec<u8>, String> {
    let cipher = Aes256Gcm::new_from_slice(master_key)
        .map_err(|_| "invalid master key length for AES-256-GCM".to_string())?;
    let mut nonce_bytes = [0u8; NONCE_LEN];
    rand::thread_rng().fill_bytes(&mut nonce_bytes);
    let nonce = Nonce::from_slice(&nonce_bytes);
    let ciphertext = cipher
        .encrypt(nonce, data_key.as_bytes().as_ref())
        .map_err(|_| "AEAD encrypt failed".to_string())?;
    let mut out = Vec::with_capacity(NONCE_LEN + ciphertext.len());
    out.extend_from_slice(&nonce_bytes);
    out.extend_from_slice(&ciphertext);
    Ok(out)
}

/// Unwrap (decrypt) a data key previously wrapped by [`wrap_data_key`].
#[instrument(name = "unwrap_data_key", skip_all)]
pub fn unwrap_data_key(
    master_key: &[u8; MASTER_KEY_LEN],
    wrapped: &[u8],
) -> Result<DataKey, String> {
    if wrapped.len() <= NONCE_LEN {
        return Err("wrapped key too short".into());
    }
    let (nonce_bytes, ciphertext) = wrapped.split_at(NONCE_LEN);
    let cipher = Aes256Gcm::new_from_slice(master_key)
        .map_err(|_| "invalid master key length for AES-256-GCM".to_string())?;
    let nonce = Nonce::from_slice(nonce_bytes);
    let plaintext = cipher
        .decrypt(nonce, ciphertext)
        .map_err(|_| "AEAD decrypt failed (tampered or wrong key)".to_string())?;
    if plaintext.len() != DATA_KEY_LEN {
        return Err(format!(
            "unwrapped data key must be {DATA_KEY_LEN} bytes, got {}",
            plaintext.len()
        ));
    }
    let mut bytes = [0u8; DATA_KEY_LEN];
    bytes.copy_from_slice(&plaintext);
    Ok(DataKey(bytes))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn master() -> [u8; MASTER_KEY_LEN] {
        [9u8; MASTER_KEY_LEN]
    }

    #[test]
    fn wrap_unwrap_round_trips() {
        let key = generate_data_key();
        let wrapped = wrap_data_key(&master(), &key).expect("wrap");
        let unwrapped = unwrap_data_key(&master(), &wrapped).expect("unwrap");
        assert_eq!(key, unwrapped);
    }

    #[test]
    fn tampered_ciphertext_fails_to_unwrap() {
        let key = generate_data_key();
        let mut wrapped = wrap_data_key(&master(), &key).expect("wrap");
        let last = wrapped.len() - 1;
        wrapped[last] ^= 0xFF;
        let err = unwrap_data_key(&master(), &wrapped).unwrap_err();
        assert!(err.contains("AEAD") || err.contains("tampered"));
    }

    #[test]
    fn distinct_data_keys_per_call() {
        let a = generate_data_key();
        let b = generate_data_key();
        assert_ne!(a.as_bytes(), b.as_bytes());
    }
}
