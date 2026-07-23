# forge-memory

Rust/Axum semantic vector-memory service (epic 17). Host port **4303**.

Step `17.01` delivers the service skeleton, health/identity endpoints, and a durable
persistence root (`vectors/` + `meta/`). Collections, upsert, and nearest-neighbor
search land in later steps.

## Local

```bash
# from repo root
make service-run SERVICE=forge-memory
make service-test SERVICE=forge-memory
```

### Manual verification

```bash
docker compose up -d forge-memory
curl -fsS localhost:4303/health/ready
curl -fsS localhost:4303/ | grep -q '"service":"forge-memory"'
docker compose restart forge-memory && curl -fsS localhost:4303/health/ready
```

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4303` (Compose: `8080`) | Listen port |
| `FORGE_MEMORY_ROOT` | `/data/memory` | Durable FS root |
| `FORGE_MEMORY_ALLOWED_BASE` | parent of root | Root must resolve under this base |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-memory` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

OpenAPI: [`contracts/openapi/forge-memory.openapi.yaml`](../../contracts/openapi/forge-memory.openapi.yaml)

### On-disk layout

```text
$FORGE_MEMORY_ROOT/
├── vectors/   # vector data (later steps)
└── meta/      # metadata index (later steps)
```

Readiness returns `200` only when the root is writable with `vectors/` and `meta/`
present and not world-writable. A world-writable root is a fatal boot error.
