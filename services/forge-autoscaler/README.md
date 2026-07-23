# forge-autoscaler

Go service that owns the `ScalingPolicy` resource kind and runs a tick-based evaluation
loop that records scaling recommendations. Port **4112**. Epic 24 / step 24.01.

This step performs **no actuation** — it never writes replica counts to Deployments or
node counts to NodePools. Later steps (`24.02`+) fill in real metric queries and desired-replica math.

## Quick start

```bash
# Requires Postgres with forge_autoscaler DB (Compose init script 06-forge-autoscaler.sh).
make -C services/forge-autoscaler run
curl -sf http://127.0.0.1:4112/health/ready
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `PORT` / `FORGE_AUTOSCALER_PORT` | `4112` (local) / `8080` (container) | Listen port |
| `FORGE_AUTOSCALER_DB_URL` | `postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable` | Own Postgres database |
| `FORGE_AUTOSCALER_EVAL_INTERVAL_MS` | `15000` | Evaluation tick interval |
| `FORGE_AUTOSCALER_METRIC_SOURCE` | `auto` | `auto` (stub adapters) or `fake` |
| `FORGE_OBSERVE_URL` | — | ObserveSource base (unused until 24.02) |
| `FORGE_GATEWAY_ADMIN_URL` | — | GatewaySource base (unused until 24.03) |
| `FORGE_EVENTS_URL` | — | QueueSource base (unused until 24.04) |
| `FORGE_RUNTIME_URL` | — | RuntimeSource fallback (unused until 24.02) |
| `FORGE_AUTH_MODE` | `dev` | Temporary until epic 09 hardening |

## API

* `POST/GET/PUT/PATCH/DELETE /v1/projects/{project}/environments/{environment}/scalingpolicies[/{name}]`
* `PUT .../scalingpolicies/{name}/status`
* `GET /v1/watch/scalingpolicies?since={resourceVersion}` — SSE `ADDED` / `MODIFIED` / `STATUS_MODIFIED` / `DELETED`
* `GET /health/live`, `GET /health/ready`

OpenAPI: [`contracts/openapi/forge-autoscaler.openapi.yaml`](../../contracts/openapi/forge-autoscaler.openapi.yaml).

## Metric sources

| Adapter | Role in 24.01 |
|---|---|
| `FakeSource` | Deterministic scripted queue for tests |
| `ObserveSource` | Stub → `ErrNotImplemented` |
| `GatewaySource` | Stub → `ErrNotImplemented` |
| `QueueSource` | Stub → `ErrNotImplemented` |
| `RuntimeSource` | Stub → `ErrNotImplemented` |

## Tests

```bash
make -C services/forge-autoscaler test-unit
```

Postgres-backed store/HTTP tests skip when `forge_autoscaler` is unreachable.
