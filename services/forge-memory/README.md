# forge-memory

Rust/Axum semantic vector-memory service (epic 17). Host port **4303**.

Step `17.05` adds text upsert/query via Forge Models embeddings on top of
project namespaces/ACL (17.04) and cosine NN (17.03). Agents consume Memory
through `memory.search` / `memory.upsert` tools.

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
P='-H X-Forge-Project:proj-a'
curl -fsS $P -XPOST $BASE/v1/collections \
  -H 'content-type: application/json' \
  -d '{"name":"incidents","dim":3,"distance":"cosine"}'
# Raw-vector path (no Models required)
curl -fsS $P -XPOST $BASE/v1/collections/incidents/upsert \
  -H 'content-type: application/json' \
  -d '{"records":[{"id":"i1","vector":[1,0,0],"metadata":{"type":"deploy"}}]}'
# Text path (requires Models at FORGE_MODELS_URL)
curl -fsS $P -XPOST $BASE/v1/collections/incidents/query \
  -H 'content-type: application/json' \
  -d '{"text":"database connection refused","model":"local-embed-small","top_k":3}' \
  | grep -q '"results"' || true
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4303` (Compose: `8080`) | Listen port |
| `FORGE_MEMORY_ROOT` | `/data/memory` | Durable FS root |
| `FORGE_MEMORY_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_MODELS_URL` | `http://forge-models:4300` | Embeddings for text upsert/query |
| `FORGE_MEMORY_DEFAULT_MODEL` | `local-embed-small` | Default embed model when omitted |
| `FORGE_MEMORY_MODELS_TIMEOUT_SECONDS` | `15` | Models HTTP timeout |
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

### API (17.05)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/v1/collections` | `{name, dim, distance:"cosine", namespace?}` → `201` |
| `GET` | `/v1/collections` | List for project (`?namespace=` filters) |
| `GET` | `/v1/collections/{name}` | Fetch (scoped) |
| `DELETE` | `/v1/collections/{name}` | Delete collection + vector file |
| `POST` | `/v1/collections/{name}/upsert` | `{records:[…]}` **or** `{items:[{id,text,metadata}], model?}` |
| `POST` | `/v1/collections/{name}/query` | `{vector,top_k}` **or** `{text,model?,top_k}` → `{results}` |
| `GET` | `/v1/collections/{name}/records` | Paginated live records |
| `GET` | `/v1/collections/{name}/records/{id}` | Get record |
| `DELETE` | `/v1/collections/{name}/records/{id}` | Tombstone (excluded from queries) |

Duplicate collection → `409`; missing / cross-project → `404`; vector/model dim ≠
collection dim or over-cap `top_k` / batch → `422`; Models down on text path →
`503 embedding_backend_unavailable` (raw-vector path unaffected); enforce
unauthenticated → `401`.

Metric: `memory_embed_calls_total` (text path).

### Benchmark (fixture scale)

Brute-force cosine at **N = 10_000**, **dim = 32** (test `bench_query_10k`):

| Metric | Measured (dev laptop) |
|---|---|
| Query latency (top_k=10, full scan) | **~27 ms** |
| Candidates scanned | 10_000 |

Reproduce: `cargo test --test bench_query_10k -- --nocapture` from
`services/forge-memory`. No hard SLA this epic; numbers are for regression
awareness.
