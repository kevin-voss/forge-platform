# forge-memory

Rust/Axum semantic vector-memory service (epic 17). Host port **4303**.

Step `17.02` adds collections with fixed-dimension vectors + JSON metadata:
mmap vector files under `vectors/`, SQLite index at `meta/index.db`, and
project-scoped REST (`X-Forge-Project`). Upsert and nearest-neighbor search
land in later steps.

## Local

```bash
# from repo root
make service-run SERVICE=forge-memory
make service-test SERVICE=forge-memory
```

### Manual verification

```bash
docker compose up -d forge-memory
BASE=localhost:4303; P='-H X-Forge-Project:proj-a'
curl -fsS $P -XPOST $BASE/v1/collections -H 'content-type: application/json' \
  -d '{"name":"incidents","dim":384,"distance":"cosine"}'
curl -fsS $P $BASE/v1/collections/incidents | grep -q '"dim":384'
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4303` (Compose: `8080`) | Listen port |
| `FORGE_MEMORY_ROOT` | `/data/memory` | Durable FS root |
| `FORGE_MEMORY_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_MEMORY_MAX_DIM` | `4096` | Sanity cap on collection dim |
| `FORGE_MEMORY_LIST_PAGE_SIZE` | `100` | Default/max page size for record list |
| `FORGE_MEMORY_MAX_METADATA_BYTES` | `65536` | Cap on serialized record metadata |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-memory` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

OpenAPI: [`contracts/openapi/forge-memory.openapi.yaml`](../../contracts/openapi/forge-memory.openapi.yaml)

### On-disk layout

```text
$FORGE_MEMORY_ROOT/
├── vectors/
│   └── <collection>.vec   # fixed-stride f32 mmap (dim × 4 bytes)
└── meta/
    └── index.db           # collections + records (id → offset, metadata)
```

Readiness returns `200` only when the root is writable with `vectors/` and `meta/`
present (not world-writable) and the SQLite metadata index is attached.

### API (17.02)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/v1/collections` | `{name, dim, distance:"cosine"}` → `201` |
| `GET` | `/v1/collections` | List for project |
| `GET` | `/v1/collections/{name}` | Fetch |
| `DELETE` | `/v1/collections/{name}` | Delete collection + vector file |
| `GET` | `/v1/collections/{name}/records` | Paginated list |
| `GET` | `/v1/collections/{name}/records/{id}` | Get record |

Duplicate collection → `409`; missing → `404`; vector length ≠ dim → `422 dimension_mismatch`
(enforced on the insert-storage primitive; upsert HTTP in 17.03).
