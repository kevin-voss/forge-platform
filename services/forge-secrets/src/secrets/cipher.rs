use crate::crypto::aead_alg::{AeadAlg, XCHACHA_NONCE_LEN};
use crate::crypto::data_key::{DataKey, NONCE_LEN};
use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use chacha20poly1305::{XChaCha20Poly1305, XNonce};
use rand::RngCore;
use tracing::instrument;

/// Ciphertext + nonce produced by [`encrypt`]. Plaintext is never retained.
#[derive(Debug, Clone)]
pub struct EncryptedValue {
    pub ciphertext: Vec<u8>,
    pub nonce: Vec<u8>,
}

/// Encrypt plaintext with the project data key. Uses a fresh random nonce per call.
#[instrument(name = "secret_encrypt", skip_all, fields(alg = alg.as_str(), plaintext_len = plaintext.len()))]
pub fn encrypt(
    alg: AeadAlg,
    data_key: &DataKey,
    plaintext: &[u8],
) -> Result<EncryptedValue, String> {
    match alg {
        AeadAlg::Aes256Gcm => {
            let cipher = Aes256Gcm::new_from_slice(data_key.as_bytes())
                .map_err(|_| "invalid data key length for AES-256-GCM".to_string())?;
            let mut nonce_bytes = [0u8; NONCE_LEN];
            rand::thread_rng().fill_bytes(&mut nonce_bytes);
            let nonce = Nonce::from_slice(&nonce_bytes);
            let ciphertext = cipher
                .encrypt(nonce, plaintext)
                .map_err(|_| "AEAD encrypt failed".to_string())?;
            Ok(EncryptedValue {
                ciphertext,
                nonce: nonce_bytes.to_vec(),
            })
        }
        AeadAlg::XChaCha20Poly1305 => {
            let cipher = XChaCha20Poly1305::new_from_slice(data_key.as_bytes())
                .map_err(|_| "invalid data key length for XChaCha20-Poly1305".to_string())?;
            let mut nonce_bytes = [0u8; XCHACHA_NONCE_LEN];
            rand::thread_rng().fill_bytes(&mut nonce_bytes);
            let nonce = XNonce::from_slice(&nonce_bytes);
            let ciphertext = cipher
                .encrypt(nonce, plaintext)
                .map_err(|_| "AEAD encrypt failed".to_string())?;
            Ok(EncryptedValue {
                ciphertext,
                nonce: nonce_bytes.to_vec(),
            })
        }
    }
}

/// Decrypt ciphertext with the project data key. Failures never return garbage plaintext.
#[instrument(name = "secret_decrypt", skip_all, fields(alg = alg.as_str(), ciphertext_len = ciphertext.len()))]
pub fn decrypt(
    alg: AeadAlg,
    data_key: &DataKey,
    ciphertext: &[u8],
    nonce: &[u8],
) -> Result<Vec<u8>, String> {
    match alg {
        AeadAlg::Aes256Gcm => {
            if nonce.len() != NONCE_LEN {
                return Err(format!(
                    "AES-256-GCM nonce must be {NONCE_LEN} bytes, got {}",
                    nonce.len()
                ));
            }
            let cipher = Aes256Gcm::new_from_slice(data_key.as_bytes())
                .map_err(|_| "invalid data key length for AES-256-GCM".to_string())?;
            let nonce = Nonce::from_slice(nonce);
            cipher
                .decrypt(nonce, ciphertext)
                .map_err(|_| "crypto_decrypt_failed".to_string())
        }
        AeadAlg::XChaCha20Poly1305 => {
            if nonce.len() != XCHACHA_NONCE_LEN {
                return Err(format!(
                    "XChaCha20-Poly1305 nonce must be {XCHACHA_NONCE_LEN} bytes, got {}",
                    nonce.len()
                ));
            }
            let cipher = XChaCha20Poly1305::new_from_slice(data_key.as_bytes())
                .map_err(|_| "invalid data key length for XChaCha20-Poly1305".to_string())?;
            let nonce = XNonce::from_slice(nonce);
            cipher
                .decrypt(nonce, ciphertext)
                .map_err(|_| "crypto_decrypt_failed".to_string())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::crypto::generate_data_key;

    #[test]
    fn encrypt_decrypt_round_trips_aes() {
        let key = generate_data_key();
        let enc = encrypt(AeadAlg::Aes256Gcm, &key, b"s3cret").expect("encrypt");
        let plain =
            decrypt(AeadAlg::Aes256Gcm, &key, &enc.ciphertext, &enc.nonce).expect("decrypt");
        assert_eq!(plain, b"s3cret");
    }

    #[test]
    fn encrypt_decrypt_round_trips_xchacha() {
        let key = generate_data_key();
        let enc = encrypt(AeadAlg::XChaCha20Poly1305, &key, b"s3cret").expect("encrypt");
        let plain = decrypt(
            AeadAlg::XChaCha20Poly1305,
            &key,
            &enc.ciphertext,
            &enc.nonce,
        )
        .expect("decrypt");
        assert_eq!(plain, b"s3cret");
    }

    #[test]
    fn distinct_nonces_per_write() {
        let key = generate_data_key();
        let a = encrypt(AeadAlg::Aes256Gcm, &key, b"same").expect("a");
        let b = encrypt(AeadAlg::Aes256Gcm, &key, b"same").expect("b");
        assert_ne!(a.nonce, b.nonce);
        assert_ne!(a.ciphertext, b.ciphertext);
    }

    #[test]
    fn wrong_data_key_fails_cleanly() {
        let key = generate_data_key();
        let other = generate_data_key();
        let enc = encrypt(AeadAlg::Aes256Gcm, &key, b"s3cret").expect("encrypt");
        let err = decrypt(AeadAlg::Aes256Gcm, &other, &enc.ciphertext, &enc.nonce).unwrap_err();
        assert_eq!(err, "crypto_decrypt_failed");
    }
}
