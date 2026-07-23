# forge-secrets

Rust/Axum secrets service (epic 10). Host port **4104**.

## Step 10.01 — skeleton + encryption key bootstrap

* Runtime contract: `/health/live`, `/health/ready`, `/`
* Master key from `FORGE_SECRETS_MASTER_KEY` (base64, 32 bytes) via `KeyProvider` / `EnvMasterKeyProvider`
* Per-project data keys generated, wrapped with AES-256-GCM, stored in `project_data_keys`
* Ready only when DB + migrations + master-key wrap/unwrap self-check succeed

## Step 10.02 — encrypted store + versioning + metadata

* `PUT /v1/projects/{pid}/envs/{env}/secrets/{name}` — encrypt value (AEAD), new version each set
* `GET .../secrets` — list metadata only (no values)
* `GET .../secrets/{name}` — metadata + version history (no values)
* `POST .../secrets/{name}:access` — authorized reveal (decrypt); audit stub for 10.06
* Values stored as ciphertext + nonce; plaintext never persisted

## Step 10.03 — config vs secrets + project isolation

* `PUT/GET/DELETE .../config/{name}` and `GET .../config` — **plaintext** config (values returned)
* Do **not** put secrets in config; use the secrets API for sensitive values
* `SecretsAuth` middleware: bearer → Identity introspect → project isolation → `authz/check`
* Distinct actions: `secret.read` / `secret.write` / `config.read` / `config.write`
* Default `FORGE_AUTH_MODE=enforce`; `dev` is an explicit insecure bypass
* Identity outage fails closed for secret writes/reveals (`503`); config reads may use cache within TTL

### Local

```bash
# from repo root
make service-run SERVICE=forge-secrets
make service-test SERVICE=forge-secrets
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4104` (Compose: `8080`) | Listen port |
| `FORGE_SECRETS_DB_URL` | `postgres://forge:forge@…/forge_secrets` | Postgres |
| `FORGE_SECRETS_MASTER_KEY` | _(required for ready)_ | base64 32-byte key; never logged |
| `FORGE_SECRETS_MASTER_KEY_ID` | `m1` | Active master key identifier |
| `FORGE_SECRETS_AEAD_ALG` | `aes-256-gcm` | or `xchacha20poly1305` |
| `FORGE_SECRETS_MAX_VALUE_BYTES` | `65536` | Reject oversized values with 413 |
| `FORGE_AUTH_MODE` | `enforce` | `dev` bypasses auth (loud warning) |
| `FORGE_IDENTITY_URL` | `http://forge-identity:4002` | Introspect + authz/check |
| `FORGE_INTROSPECT_CACHE_TTL_S` | `10` | Short TTL so revocation is honored |

### Nonce management

Wrapped keys and secret values use AEAD with a **fresh random nonce per write**. AES-256-GCM uses 12-byte nonces; XChaCha20-Poly1305 uses 24-byte nonces. Ciphertext and nonce are stored separately; plaintext is never retained.
