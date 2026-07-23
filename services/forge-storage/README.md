# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.03 — streamed upload / download

* `PUT /v1/buckets/{bucket}/objects/{key}` — stream body → temp → fsync → atomic rename
* `GET /v1/buckets/{bucket}/objects/{key}` — stream bytes with `Content-Type` / `Content-Length`
* `HEAD /v1/buckets/{bucket}/objects/{key}` — metadata headers only
* Bounded memory via fixed buffer (`FORGE_STORAGE_STREAM_BUFFER_BYTES`, default 64 KiB)
* Overwrite is last-write-wins and atomic (readers never see partial objects)
* Interim on-disk layout: `objects/<project_id>/<bucket_id>/<sha256(key)>` (content-addressed blobs in 13.04)
* OpenAPI: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

SHA-256 verification, range requests, signed tokens, and quotas land in later steps.

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Upload / download (dev mode)

```bash
BASE=localhost:4107
P='-H X-Forge-Project:proj-a'
curl -fsS -XPOST $BASE/v1/buckets -H 'X-Forge-Project: proj-a' \
  -H 'content-type: application/json' -d '{"name":"artifacts"}'
head -c 5000000 /dev/urandom > /tmp/big.bin
curl -fsS $P -XPUT --data-binary @/tmp/big.bin \
  -H 'content-type: application/octet-stream' \
  "$BASE/v1/buckets/artifacts/objects/big.bin"
curl -fsS $P "$BASE/v1/buckets/artifacts/objects/big.bin" -o /tmp/big.out
cmp /tmp/big.bin /tmp/big.out && echo "round-trip OK"
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
│   └── <project_id>/<bucket_id>/<key-sha256>   # interim (13.03)
└── meta/
    ├── index.db      # SQLite: buckets + objects metadata
    └── tmp/          # in-flight uploads (cleaned on failure)
```

### Naming

* Bucket: 3–63 chars, `a-z0-9-`, start/end alphanumeric; no path separators / `..` / NUL; reserved: `meta`, `objects`
* Object key: 1–1024 chars; no leading `/`, no `..` segments, no NUL
