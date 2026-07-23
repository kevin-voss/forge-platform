# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.06 — Quotas, delete, restart durability

* Per-project byte quota (`FORGE_STORAGE_DEFAULT_QUOTA_BYTES`, default 1 GiB) with optional `project_quota` override
* Upload exceeding quota → `413` + `quota_exceeded` (metadata not committed; new blob cleaned)
* `DELETE /v1/buckets/{bucket}/objects/{key}` → `204`; blob GC only when refcount hits 0
* `DELETE /v1/buckets/{bucket}?force=true` cascade-deletes objects then bucket (`409` without force when non-empty)
* `GET /v1/usage` → `{project_id, used_bytes, quota_bytes, objects}`
* Boot reconciler rebuilds usage + blob refcounts; orphan blobs GC'd (`FORGE_STORAGE_RECONCILE_ON_BOOT`)
* OpenAPI: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Usage + delete (dev mode)

```bash
BASE=localhost:4107
P='-H X-Forge-Project:proj-a'
curl -fsS $P "$BASE/v1/usage"
curl -fsS $P -XDELETE "$BASE/v1/buckets/artifacts/objects/big.bin" -o /dev/null -w '%{http_code}\n'  # 204
curl -fsS $P -XDELETE "$BASE/v1/buckets/artifacts?force=true" -o /dev/null -w '%{http_code}\n'   # 204 cascade
docker compose restart forge-storage
curl -fsS $P "$BASE/v1/buckets"   # buckets/objects on named volume survive restart
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4107` (Compose: `8080`) | Listen port |
| `FORGE_STORAGE_ROOT` | `/data/storage` | Durable FS root |
| `FORGE_STORAGE_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_STORAGE_META_PATH` | `$FORGE_STORAGE_ROOT/meta/index.db` | SQLite metadata index |
| `FORGE_STORAGE_STREAM_BUFFER_BYTES` | `65536` | Upload/download chunk size |
| `FORGE_STORAGE_MAX_OBJECT_BYTES` | unset | Optional hard upload cap |
| `FORGE_STORAGE_DEFAULT_QUOTA_BYTES` | `1073741824` | Default per-project quota (1 GiB) |
| `FORGE_STORAGE_RECONCILE_ON_BOOT` | `true` | Rebuild usage/refcounts + GC orphans |
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

Per-project quota overrides live in the `project_quota` table (admin/internal path; Identity role gating later).

### On-disk layout

```text
$FORGE_STORAGE_ROOT/
├── objects/
│   └── <aa>/<sha256>     # content-addressed blob (refcount GC on delete)
└── meta/
    ├── index.db          # SQLite: buckets, objects, blobs, project_quota, project_usage
    └── tmp/              # in-flight uploads (cleaned on failure)
```

### Naming

* Bucket: 3–63 chars, `a-z0-9-`, start/end alphanumeric; no path separators / `..` / NUL; reserved: `meta`, `objects`
* Object key: 1–1024 chars; no leading `/`, no `..` segments, no NUL
