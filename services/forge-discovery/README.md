# forge-discovery

Platform service discovery directory (epic 21). Host port `4109` / container `8080`.

This step (`21.01`) ships the runnable skeleton: health probes, graceful shutdown,
OTEL wiring, the `discovery` Postgres schema (`services` / `endpoints`), and
idempotent `Service` / `Endpoint` kind registration against Control's
`POST /v1/kinds` facade. Registration, leases, selection, and DNS land in later
steps.

## Quick start

```bash
# From repo root
make -C services/forge-discovery run

curl -sf localhost:4109/health/live | jq
curl -sf localhost:4109/health/ready | jq
curl -s localhost:4001/v1/kinds | jq '.[] | select(.plural=="endpoints" or .plural=="services")'
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | Container listen port (host `4109`) |
| `FORGE_SERVICE_NAME` | `forge-discovery` | Identity + logs |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Metric `forge_discovery_up{version}` |
| `FORGE_LOG_LEVEL` | `info` | debug\|info\|warn\|error |
| `FORGE_ENV` | `development` | Deployment environment label |
| `FORGE_AUTH_MODE` | `dev` | Until epic 09 mTLS |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |
| `FORGE_DATABASE_URL` | `postgres://forge:forge@postgres:5432/forge?sslmode=disable` | Shared Postgres |
| `FORGE_DATABASE_SCHEMA` | `discovery` | Authoritative serving store |
| `FORGE_DATABASE_POOL_MAX` | `10` | pgx pool size |
| `FORGE_DATABASE_MIGRATE_ON_START` | `true` | Fail fast on migration errors |
| `FORGE_CONTROL_URL` | `http://forge-control:8080` | Kind registration target |
| `FORGE_OTEL_ENABLED` | `true` | OTLP export |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://otel-collector:4317` | OTLP gRPC |

## Health

* `GET /health/live` → `200 {"status":"ok"}` while the process is up
* `GET /health/ready` → `200 {"status":"ok"}` after DB pool + Control kind registration succeed; otherwise `503 {"status":"not_ready"}`

## Persistence

Schema `discovery` is the fast, authoritative-for-serving store. Control's generic
resource store receives an async mirror in later steps — not the hot path.
