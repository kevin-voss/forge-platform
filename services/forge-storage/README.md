# forge-storage

Rust/Axum object storage service (epic 13). Host port **4107**.

## Step 13.01 — skeleton + local FS backend

* Runtime contract: `/health/live`, `/health/ready`, `/`
* `LocalFsBackend` initializes `FORGE_STORAGE_ROOT` with `objects/` and `meta/`
* Readiness gated on a writable storage root (retry with backoff if unavailable)
* Refuses to start if the root resolves outside `FORGE_STORAGE_ALLOWED_BASE` or is world-writable
* Compose named volume `forge-storage-data` for restart durability
* OpenAPI skeleton: [`contracts/openapi/forge-storage.openapi.yaml`](../../contracts/openapi/forge-storage.openapi.yaml)

Bucket/object APIs, streaming, checksums, signed tokens, and quotas land in later steps.

### Local

```bash
# from repo root
make service-run SERVICE=forge-storage
make service-test SERVICE=forge-storage
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4107` (Compose: `8080`) | Listen port |
| `FORGE_STORAGE_ROOT` | `/data/storage` | Durable FS root |
| `FORGE_STORAGE_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-storage` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

### On-disk layout

```text
$FORGE_STORAGE_ROOT/
├── objects/     # content-addressed blobs (later steps)
└── meta/        # metadata index home (later steps)
```
