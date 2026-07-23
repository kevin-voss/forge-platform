# forge-memory

Rust/Axum semantic vector-memory service (epic 17). Host port **4303**.

Step `17.04` adds project namespaces and Identity-backed access control on top of
upsert/query (17.03): every collection/record/query is scoped by `project_id`
(+ optional `namespace`), with cross-project access returning `404`.

## Local

```bash
# from repo root
make service-run SERVICE=forge-memory
make service-test SERVICE=forge-memory
```

### Manual verification

```bash
docker compose up -d forge-memory
BASE=localhost:4303
curl -fsS -H 'X-Forge-Project: proj-a' -XPOST $BASE/v1/collections \
  -H 'content-type: application/json' \
  -d '{"name":"incidents","dim":3,"distance":"cosine"}'
curl -fsS -H 'X-Forge-Project: proj-a' $BASE/v1/collections | grep -q incidents
test "$(curl -fsS -H 'X-Forge-Project: proj-b' $BASE/v1/collections | grep -c incidents)" = 0
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4303` (Compose: `8080`) | Listen port |
| `FORGE_MEMORY_ROOT` | `/data/memory` | Durable FS root |
| `FORGE_MEMORY_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_AUTH_MODE` | `dev` | `dev` (header) or `enforce`/`enforced` (Identity) |
| `FORGE_IDENTITY_URL` | — | Required when `FORGE_AUTH_MODE=enforce` |
| `FORGE_INTROSPECT_CACHE_TTL_S` | `10` | Introspect cache TTL |
| `FORGE_MEMORY_MAX_DIM` | `4096` | Sanity cap on collection dim |
| `FORGE_MEMORY_LIST_PAGE_SIZE` | `100` | Default/max page size for record list |
| `FORGE_MEMORY_MAX_METADATA_BYTES` | `65536` | Cap on serialized record metadata |
| `FORGE_MEMORY_MAX_TOP_K` | `100` | Cap on query `top_k` |
| `FORGE_MEMORY_MAX_UPSERT_BATCH` | `512` | Cap on upsert batch size |
| `FORGE_MEMORY_COMPACT_ON_BOOT` | `true` | Reclaim tombstoned vector slots at start |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-memory` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

OpenAPI: [`contracts/openapi/forge-memory.openapi.yaml`](../../contracts/openapi/forge-memory.openapi.yaml)

### Auth + namespaces

* **Dev** (`FORGE_AUTH_MODE=dev`): require `X-Forge-Project` (logged as insecure bypass).
* **Enforce**: require `Authorization: Bearer …`; Identity introspect must be active
  and permit the project. Missing/invalid token → `401`. Read-only roles (`viewer`,
  etc.) → `403` on writes. Cross-project ids → `404` (no existence leak).
* Optional `?namespace=` (e.g. `agent-memory`, `docs`) isolates collections within
  a project. Uniqueness is `(project_id, namespace, name)`. Unknown namespace is
  treated as empty for lookups (miss → `404`).

### On-disk layout

```text
$FORGE_MEMORY_ROOT/
├── vectors/
│   └── <project>/<namespace|_default>/<collection>.vec
└── meta/
    └── index.db           # collections + records (project + namespace scoped)
```

Readiness returns `200` only when the root is writable with `vectors/` and `meta/`
present (not world-writable) and the SQLite metadata index is attached.

### API (17.04)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/v1/collections` | `{name, dim, distance:"cosine", namespace?}` → `201` |
| `GET` | `/v1/collections` | List for project (`?namespace=` filters) |
| `GET` | `/v1/collections/{name}` | Fetch (scoped) |
| `DELETE` | `/v1/collections/{name}` | Delete collection + vector file |
| `POST` | `/v1/collections/{name}/upsert` | Batch `{records:[{id,vector,metadata}]}` → `{upserted}` |
| `POST` | `/v1/collections/{name}/query` | `{vector, top_k, filter?}` → ranked `{results}` |
| `GET` | `/v1/collections/{name}/records` | Paginated live records |
| `GET` | `/v1/collections/{name}/records/{id}` | Get record |
| `DELETE` | `/v1/collections/{name}/records/{id}` | Tombstone (excluded from queries) |

Duplicate collection → `409`; missing / cross-project → `404`; vector length ≠ dim
or over-cap `top_k` / batch → `422`; enforce unauthenticated → `401`.

### Benchmark (fixture scale)

Brute-force cosine at **N = 10_000**, **dim = 32** (test `bench_query_10k`):

| Metric | Measured (dev laptop) |
|---|---|
| Query latency (top_k=10, full scan) | **~27 ms** |
| Candidates scanned | 10_000 |

Reproduce: `cargo test --test bench_query_10k -- --nocapture` from
`services/forge-memory`. No hard SLA this epic; numbers are for regression
awareness.
