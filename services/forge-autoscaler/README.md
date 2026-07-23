# forge-autoscaler

Go service that owns the `ScalingPolicy` resource kind and runs a tick-based evaluation
loop that recommends and actuates workload replica counts. Port **4112**. Epic 24 / step 24.02.

CPU and memory policies compute desired replicas (`ceil(current * metric / target)`), apply
stabilization windows and `maxReplicasPerMinute` rate limits, then patch
`Application.spec.scaling.desiredReplicas` through the Control resource API.

## Quick start

```bash
# Requires Postgres with forge_autoscaler DB (Compose init script 06-forge-autoscaler.sh).
make -C services/forge-autoscaler run
curl -sf http://127.0.0.1:4112/health/ready
curl -sf http://127.0.0.1:4112/metrics | head
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `PORT` / `FORGE_AUTOSCALER_PORT` | `4112` (local) / `8080` (container) | Listen port |
| `FORGE_AUTOSCALER_DB_URL` | `postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable` | Own Postgres database |
| `FORGE_AUTOSCALER_EVAL_INTERVAL_MS` | `15000` | Evaluation tick interval |
| `FORGE_AUTOSCALER_METRIC_SOURCE` | `auto` | `auto` (Observe→Runtime) or `fake` |
| `FORGE_OBSERVE_URL` | — | Prometheus-compatible query base for ObserveSource |
| `FORGE_RUNTIME_URL` | — | RuntimeSource local fallback |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Application resource API for actuation |
| `FORGE_GATEWAY_ADMIN_URL` | — | GatewaySource base (unused until 24.03) |
| `FORGE_EVENTS_URL` | — | QueueSource base (unused until 24.04) |
| `FORGE_AUTH_MODE` | `dev` | Temporary until epic 09 hardening |

## API

* `POST/GET/PUT/PATCH/DELETE /v1/projects/{project}/environments/{environment}/scalingpolicies[/{name}]`
* `PUT .../scalingpolicies/{name}/status`
* `GET /v1/watch/scalingpolicies?since={resourceVersion}` — SSE `ADDED` / `MODIFIED` / `STATUS_MODIFIED` / `DELETED`
* `GET /health/live`, `GET /health/ready`
* `GET /metrics` — `forge_autoscaler_recommendation_replicas`, `forge_autoscaler_scale_actions_total`

OpenAPI: [`contracts/openapi/forge-autoscaler.openapi.yaml`](../../contracts/openapi/forge-autoscaler.openapi.yaml).

## Metric sources

| Adapter | Role in 24.02 |
|---|---|
| `FakeSource` | Deterministic scripted queue for tests |
| `ObserveSource` | PromQL instant query for `cpu` / `memory` (`forge_workload_*_utilization`) |
| `RuntimeSource` | Local fallback from `/v1/node` + `/v1/node/state` when Observe is down |
| `GatewaySource` | Stub → `ErrNotImplemented` (24.03) |
| `QueueSource` | Stub → `ErrNotImplemented` (24.04) |

## Scaling behaviour

* Utilization math: `ceil(currentReplicas * currentMetric / targetMetric)`, clamped to `[minReplicas, maxReplicas]`
* Stabilization: scale-up and scale-down windows keep the highest recommendation in-window (prevents rapid downscale)
* Rate limit: `behavior.scaleUp/Down.maxReplicasPerMinute`
* Metric outage: hold last safe desired (≥ `minReplicas`), `ScalingActive=Unknown`
* Actuation: merge-patch Application; 409 retries with read-refresh and the same operation id

## Tests

```bash
make -C services/forge-autoscaler test-unit
```

Postgres-backed store/HTTP tests skip when `forge_autoscaler` is unreachable.
