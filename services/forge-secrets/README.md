# forge-secrets

Rust/Axum secrets service (epic 10). Host port **4104**.

## Step 10.01 — skeleton + encryption key bootstrap

* Runtime contract: `/health/live`, `/health/ready`, `/`
* Master key from `FORGE_SECRETS_MASTER_KEY` (base64, 32 bytes) via `KeyProvider` / `EnvMasterKeyProvider`
* Per-project data keys generated, wrapped with AES-256-GCM, stored in `project_data_keys`
* Ready only when DB + migrations + master-key wrap/unwrap self-check succeed

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

### Nonce management

Wrapped keys use AES-256-GCM with a **random 12-byte nonce prepended** to each ciphertext. Nonces must never be reused with the same master key; random generation per wrap provides that guarantee for this bootstrap.
