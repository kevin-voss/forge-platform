# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.05 — Signed access tokens + expiry

* `POST /v1/buckets/{bucket}/objects/{key}/sign` with `{method, ttl_seconds}` → `{token, url, expires_at}`
* Token = URL-safe base64 JSON claims + HMAC-SHA256 over canonical `(method, project_id, bucket, key, exp)`
* GET/PUT accept `?token=` or `Authorization: Bearer <token>` without project credentials
* Expired → `401 token_expired`; method/scope mismatch → `403`; tampered → `401 invalid_token`
* TTL rejected above `FORGE_STORAGE_MAX_TTL_SECONDS` (`ttl_too_large`)
* Optional `FORGE_STORAGE_SIGNING_KEY_PREV` for verify-only during key rotation
* OpenAPI: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

Quotas and hard delete land in 13.06.

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Sign + download (dev mode)

```bash
BASE=localhost:4107
P='-H X-Forge-Project:proj-a'
export FORGE_STORAGE_SIGNING_KEY=dev-only-signing-key-change-me
# ensure bucket + object exist first (see 13.04 examples)
TOK=$(curl -fsS $P -XPOST -H 'content-type: application/json' \
  -d '{"method":"GET","ttl_seconds":2}' \
  "$BASE/v1/buckets/artifacts/objects/big.bin/sign" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -fsS "$BASE/v1/buckets/artifacts/objects/big.bin?token=$TOK" -o /dev/null && echo "valid token OK"
sleep 3
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/buckets/artifacts/objects/big.bin?token=$TOK")
test "$code" = 401 && echo "expired token rejected OK"
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4107` (Compose: `8080`) | Listen port |
| `FORGE_STORAGE_ROOT` | `/data/storage` | Durable FS root |
| `FORGE_STORAGE_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_STORAGE_META_PATH` | `$FORGE_STORAGE_ROOT/meta/index.db` | SQLite metadata index |
| `FORGE_STORAGE_STREAM_BUFFER_BYTES` | `65536` | Upload/download chunk size |
| `FORGE_STORAGE_MAX_OBJECT_BYTES` | unset | Optional hard upload cap (quotas in 13.06) |
| `FORGE_STORAGE_VERIFY_ON_READ` | `off` | `off` or `full` (re-hash on GET) |
| `FORGE_STORAGE_SIGNING_KEY` | — | HMAC secret; required when `FORGE_AUTH_MODE=enforce` |
| `FORGE_STORAGE_SIGNING_KEY_PREV` | — | Optional previous key (verify only) |
| `FORGE_STORAGE_MAX_TTL_SECONDS` | `3600` | Max TTL for `POST .../sign` |
| `FORGE_STORAGE_CLOCK_SKEW_SECONDS` | `30` | Expiry tolerance |
| `FORGE_AUTH_MODE` | `dev` | `dev` (header) or `enforce`/`enforced` (Identity) |
| `FORGE_IDENTITY_URL` | — | Required when `FORGE_AUTH_MODE=enforce` |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-storage` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

### On-disk layout

```text
$FORGE_STORAGE_ROOT/
├── objects/
│   └── <aa>/<sha256>     # content-addressed blob (13.04)
└── meta/
    ├── index.db          # SQLite: buckets + objects + blobs.refcount
    └── tmp/              # in-flight uploads (cleaned on failure)
```

### Naming

* Bucket: 3–63 chars, `a-z0-9-`, start/end alphanumeric; no path separators / `..` / NUL; reserved: `meta`, `objects`
* Object key: 1–1024 chars; no leading `/`, no `..` segments, no NUL
