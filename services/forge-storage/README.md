# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.02 — buckets, metadata index, project isolation

* Bucket APIs: `POST/GET /v1/buckets`, `GET/DELETE /v1/buckets/{bucket}`
* Metadata SQLite index at `$FORGE_STORAGE_ROOT/meta/index.db` (override with `FORGE_STORAGE_META_PATH`)
* Project isolation via `X-Forge-Project` (`FORGE_AUTH_MODE=dev`, default) or Identity bearer (`enforce`)
* Cross-project get/delete returns **404** (no existence leak)
* Delete non-empty bucket → **409** with `object_count`
* OpenAPI: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

Object byte upload/download, checksums, signed tokens, and quotas land in later steps.

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Bucket lifecycle (dev mode)

```bash
BASE=localhost:4107
curl -fsS -XPOST $BASE/v1/buckets -H 'X-Forge-Project: proj-a' \
  -H 'content-type: application/json' -d '{"name":"artifacts"}'
curl -fsS $BASE/v1/buckets -H 'X-Forge-Project: proj-a'
# isolation: project b sees nothing
curl -fsS $BASE/v1/buckets -H 'X-Forge-Project: proj-b'
curl -fsS -XDELETE $BASE/v1/buckets/artifacts -H 'X-Forge-Project: proj-a' -o /dev/null -w '%{http_code}\n'
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4107` (Compose: `8080`) | Listen port |
| `FORGE_STORAGE_ROOT` | `/data/storage` | Durable FS root |
| `FORGE_STORAGE_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_STORAGE_META_PATH` | `$FORGE_STORAGE_ROOT/meta/index.db` | SQLite metadata index |
| `FORGE_AUTH_MODE` | `dev` | `dev` (header) or `enforce`/`enforced` (Identity) |
| `FORGE_IDENTITY_URL` | — | Required when `FORGE_AUTH_MODE=enforce` |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-storage` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

### On-disk layout

```text
$FORGE_STORAGE_ROOT/
├── objects/          # content-addressed blobs (later steps)
└── meta/
    └── index.db      # SQLite: buckets + objects metadata
```

### Naming

* Bucket: 3–63 chars, `a-z0-9-`, start/end alphanumeric; no path separators / `..` / NUL; reserved: `meta`, `objects`
* Object key (validated for later upload): 1–1024 chars; no leading `/`, no `..` segments, no NUL
