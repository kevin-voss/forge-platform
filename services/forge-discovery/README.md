# forge-discovery

Platform service discovery directory (epic 21). Host port `4109` / container `8080`.

Step `21.02` adds endpoint registration with TTL leases, a background sweeper that
expires unrenewed leases, a node-watch path that marks all endpoints on an
unreachable node `Unready` in one transaction, an async Control mirror worker,
and Runtime's outbound register/renew/deregister client.

## Quick start

```bash
# From repo root
make -C services/forge-discovery run

curl -sf localhost:4109/health/live | jq
curl -sf localhost:4109/health/ready | jq

curl -s -X POST localhost:4109/v1/projects/demo/environments/local/services/demo-echo/endpoints \
  -H 'content-type: application/json' \
  -d '{"id":"demo-echo-abc123-0","node":"node-a","address":{"ip":"172.20.0.10","port":8080},"leaseSeconds":20}' | jq

curl -s -X POST localhost:4109/v1/projects/demo/environments/local/endpoints/demo-echo-abc123-0/renew \
  -H 'content-type: application/json' -d '{"ready":true,"leaseSeconds":20}' | jq '.phase'
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
| `FORGE_CONTROL_URL` | `http://forge-control:8080` | Kind registration + mirror + node watch |
| `FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT` | `20` | Default lease TTL |
| `FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS` | `5` | Expire/reap loop cadence |
| `FORGE_DISCOVERY_REAP_AFTER_SECONDS` | `300` | GC long-`Unready` endpoints |
| `FORGE_DISCOVERY_NODE_WATCH_RESYNC_SECONDS` | `30` | Full resync if watch drops |
| `FORGE_OTEL_ENABLED` | `true` | OTLP export |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://otel-collector:4317` | OTLP gRPC |

## HTTP API (21.02)

* `POST /v1/projects/{project}/environments/{environment}/services/{service}/endpoints` — register (idempotent upsert by replica id)
* `POST /v1/projects/{project}/environments/{environment}/endpoints/{id}/renew` — renew lease + readiness
* `DELETE /v1/projects/{project}/environments/{environment}/endpoints/{id}` — deregister (`204`)
* `GET /v1/projects/{project}/environments/{environment}/services/{service}/endpoints` — list (all phases; readiness-filtered selection is 21.03)

## Health

* `GET /health/live` → `200 {"status":"ok"}` while the process is up
* `GET /health/ready` → `200 {"status":"ok"}` after DB pool + Control kind registration succeed; otherwise `503 {"status":"not_ready"}`

## Persistence

Schema `discovery` is the fast, authoritative-for-serving store. Lease columns
(`ready`, `lease_seconds`, `expires_at`, `unready_reason`) live on
`discovery.endpoints`. Control's generic resource store receives an async mirror
of accepted writes (eventually consistent; retried on Control outage).
