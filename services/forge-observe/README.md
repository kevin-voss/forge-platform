# forge-observe

Go HTTP service (host port **4106**) that fronts the foundation telemetry
backends (Loki, Tempo, Prometheus) and publishes the platform **correlation
contract**.

## Endpoints (12.01)

| Path | Role |
|---|---|
| `GET /health/live` | Process liveness |
| `GET /health/ready` | Ready when required backends are reachable |
| `GET /` | Identity `{service,language,status}` |
| `GET /v1/health/backends` | `{loki,tempo,prometheus}` → `ok` \| `down` |

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
which dependency failed. Backend clients enforce `FORGE_BACKEND_TIMEOUT_MS`
and never hang indefinitely.
