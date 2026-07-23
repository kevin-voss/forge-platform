# forge-autoscaler

Go service that owns the `ScalingPolicy` resource kind and runs a tick-based evaluation
loop that recommends and actuates workload replica counts. Port **4112**. Epic 24 / step 24.05.

CPU/memory, traffic, and queue-depth worker policies compute desired replicas, then apply
**schedules**, **manual overrides**, **deployment freezes**, and **metric-outage fallbacks**
before patching `Application` or `Worker` `spec.scaling.desiredReplicas` through the Control
resource API.

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
| `FORGE_AUTOSCALER_METRIC_SOURCE` | `auto` | `auto` (Observe/Gateway/Queue/Runtime) or `fake` |
| `FORGE_OBSERVE_URL` | — | Prometheus-compatible query base for ObserveSource |
| `FORGE_RUNTIME_URL` | — | RuntimeSource local fallback |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Application/Worker resource API for actuation |
| `FORGE_GATEWAY_ADMIN_URL` | — | GatewaySource base (`GET /admin/metrics?application=`) |
| `FORGE_EVENTS_URL` | — | QueueSource + audit event publish base |
| `FORGE_AUTH_MODE` | `dev` | Temporary until epic 09 hardening |

## API

* `POST/GET/PUT/PATCH/DELETE /v1/projects/{project}/environments/{environment}/scalingpolicies[/{name}]`
* `PUT .../scalingpolicies/{name}/status`
* `PUT/GET/DELETE .../scalingpolicies/{name}/override` — manual override subresource (TTL + reason)
* `GET /v1/watch/scalingpolicies?since={resourceVersion}` — SSE `ADDED` / `MODIFIED` / `STATUS_MODIFIED` / `DELETED`
* `GET /health/live`, `GET /health/ready`
* `GET /metrics` — recommendation, scale actions, queue backlog, `forge_autoscaler_schedule_active`, `forge_autoscaler_manual_override_active`

OpenAPI: [`contracts/openapi/forge-autoscaler.openapi.yaml`](../../contracts/openapi/forge-autoscaler.openapi.yaml).

Ops runbook: [`docs/operations/autoscaler-overrides.md`](../../docs/operations/autoscaler-overrides.md).

## Metric sources

| Adapter | Role |
|---|---|
| `FakeSource` | Deterministic scripted queue for tests |
| `ObserveSource` | PromQL for `cpu` / `memory` / `p95Latency` / `errorRate`; historical fallback for traffic |
| `RuntimeSource` | Local fallback from `/v1/node` + `/v1/node/state` when Observe is down (cpu/memory) |
| `GatewaySource` | `GET /admin/metrics?application=` for `httpRequests` / `activeConnections` |
| `QueueSource` | `GET /admin/metrics?queue=` for `queueDepth`, oldest age, lag, retry, processing duration, DLQ pressure |

## Scaling behaviour

* Utilization / traffic / queue math as in 24.02–24.04, clamped to effective `[min,max]`
* **Schedules** — cron + timezone (+ optional `endTime`) raise/lower effective bounds; overlapping schedules merge to highest min + lowest max
* **Manual override** — supersedes metrics until TTL; emits `autoscaling.override.created` / `.expired`
* **Deployment freeze** — `spec.deploymentFreeze` or target `status.phase=Progressing` blocks scale-down only
* **Metric outage** — `metricOutageFallback.mode` = `hold` (default) / `floor` / `fixed`; surfaced as `status.metricOutageMode`
* Stabilization windows + `maxReplicasPerMinute` rate limits
* Actuation: merge-patch Application or Worker; 409 retries with read-refresh

## Tests

```bash
make -C services/forge-autoscaler test-unit
```

Postgres-backed store/HTTP tests skip when `forge_autoscaler` is unreachable.
