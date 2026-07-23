# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.04 — SHA-256 integrity + byte-range requests

* `PUT` hashes while streaming; response includes `sha256`
* Blobs stored content-addressed: `objects/<aa>/<sha256>` (identical content → dedup hit)
* Optional `X-Expected-SHA256` → `422 checksum_mismatch` when content differs
* `GET`/`HEAD` return `ETag: "<sha256>"`, `X-Content-SHA256`, `Accept-Ranges: bytes`
* `Range: bytes=` → `206` + `Content-Range`; unsatisfiable → `416`
* `FORGE_STORAGE_VERIFY_ON_READ=full` re-hashes on read → `500 integrity_error` on corruption
* OpenAPI: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

Signed tokens and quotas land in later steps.

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Upload / checksum / range (dev mode)

```bash
BASE=localhost:4107
P='-H X-Forge-Project:proj-a'
curl -fsS -XPOST $BASE/v1/buckets -H 'X-Forge-Project: proj-a' \
  -H 'content-type: application/json' -d '{"name":"artifacts"}'
head -c 5000000 /dev/urandom > /tmp/big.bin
curl -fsS $P -XPUT --data-binary @/tmp/big.bin \
  -H 'content-type: application/octet-stream' \
  "$BASE/v1/buckets/artifacts/objects/big.bin" | grep -o '"sha256":"[0-9a-f]*"'
LOCAL=$(shasum -a 256 /tmp/big.bin | awk '{print $1}')
SRV=$(curl -fsSI $P "$BASE/v1/buckets/artifacts/objects/big.bin" | tr -d '\r' | awk -F'"' '/ETag/{print $2}')
test "$LOCAL" = "$SRV" && echo "checksum OK"
curl -fsS $P -H 'Range: bytes=0-1023' \
  "$BASE/v1/buckets/artifacts/objects/big.bin" -o /tmp/range.bin -w '%{http_code}\n'
test "$(wc -c < /tmp/range.bin | tr -d ' ')" = 1024 && echo "range OK"
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
