# forge-observe

Go HTTP service (host port **4106**) that fronts the foundation telemetry
backends (Loki, Tempo, Prometheus) and publishes the platform **correlation
contract**.

## Endpoints

| Path | Role |
|---|---|
| `GET /health/live` | Process liveness |
| `GET /health/ready` | Ready when required backends are reachable |
| `GET /` | Identity `{service,language,status}` |
| `GET /v1/health/backends` | `{loki,tempo,prometheus}` → `ok` \| `down` |
| `GET /v1/logs` | Correlated log query (12.04) |
| `GET /v1/logs/stream` | Live log tail via SSE (12.05) |

## Log query (`GET /v1/logs`)

Filters (at least one scoping filter required):

* `project`, `deployment`, `service`, `request_id`, `trace_id`
* `since` / `until` (RFC3339 or unix), `q` (escaped free text)
* `limit`, `direction` (`forward`\|`backward`), `cursor`

Response entries are normalized to the correlation field set (`time`, `service`,
`trace_id`, `request_id`, `level`, `message`, `deployment`, `project`, …).
Querying by `trace_id` returns logs from all services in that trace, time-ordered.

Caps (clamp + `warnings` / `capped`):

* `FORGE_LOG_QUERY_MAX_LIMIT` (default 1000)
* `FORGE_LOG_QUERY_MAX_RANGE_H` (default 24)

When `FORGE_AUTH_MODE=enforce`, queries require a bearer token and
`project.read` on the requested project (Identity).

## Log stream (`GET /v1/logs/stream`)

Same scoping filters as the query API (plus `since` / `q`). Response is
`text/event-stream` with `event: log` and a `LogEntry` JSON `data` payload.
The handler probes Loki before committing to SSE and returns `503`
`loki_unavailable` when the backend is down so `forge logs --follow` can fall
back to Runtime (`04.05`) for single-service targets.

CLI:

```bash
forge logs --project prj_1 --deployment dpl_1            # point-in-time
forge logs --service demo --follow                       # live tail
forge logs --trace-id "$TRACE" --follow --json
```

Env: `FORGE_OBSERVE_URL`, `FORGE_LOGS_RECONNECT_MS` (default 1000),
`FORGE_LOGS_FALLBACK` (`observe`|`runtime`|`auto`).

## Configuration

| Variable | Default |
|---|---|
| `PORT` | `4106` |
| `FORGE_SERVICE_NAME` | `forge-observe` |
| `FORGE_LOKI_URL` | `http://loki:3100` |
| `FORGE_TEMPO_URL` | `http://tempo:3200` |
| `FORGE_PROMETHEUS_URL` | `http://prometheus:9090` |
| `FORGE_BACKEND_TIMEOUT_MS` | `2000` |
| `FORGE_OBSERVE_READY_REQUIRE_BACKENDS` | `loki,tempo,prometheus` |
| `FORGE_LOG_QUERY_MAX_LIMIT` | `1000` |
| `FORGE_LOG_QUERY_MAX_RANGE_H` | `24` |
| `FORGE_AUTH_MODE` | `dev` |
| `FORGE_IDENTITY_URL` | `http://forge-identity:4002` |

## Correlation contract

* Doc: [`docs/contracts/observability-correlation.md`](../../docs/contracts/observability-correlation.md)
* Go constants: `internal/correlation`

## Grafana dashboards (12.03)

Provisioned under `deploy/observability/grafana/` (see
[`docs/operations/grafana-dashboards.md`](../../docs/operations/grafana-dashboards.md)):

* Forge Platform / Service / Deployment / Runtime
* Provider: `provisioning/dashboards/forge.yaml`
* Checks: `make -C services/forge-observe test-dashboards`

## Local commands

```bash
make -C services/forge-observe test-unit
make -C services/forge-observe test-dashboards
make -C services/forge-observe run          # Compose on :4106
make -C services/forge-observe test         # unit + dashboards + integration
```

## Degraded mode

When a required backend is unreachable, `/health/ready` returns 503 while
`/health/live` and `/v1/health/backends` stay available so operators can see
which dependency failed. `GET /v1/logs` returns `503` with `loki_unavailable`
when Loki is down. Backend clients enforce `FORGE_BACKEND_TIMEOUT_MS`
and never hang indefinitely.
