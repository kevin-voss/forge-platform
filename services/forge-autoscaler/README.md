# forge-autoscaler

Go service that owns the `ScalingPolicy` resource kind and runs tick-based evaluation
loops for workload replicas and **node scale-up / scale-down**. Port **4112**. Epic 24 / steps 24.01–24.07.

CPU/memory, traffic, and queue-depth worker policies compute desired replicas, then apply
**schedules**, **manual overrides**, **deployment freezes**, and **metric-outage fallbacks**
before patching `Application` or `Worker` `spec.scaling.desiredReplicas` through the Control
resource API.

The **node autoscaler** loop reads pending placements, cluster reservation, and fleet
utilization from Control; selects an eligible `NodePool`; writes `status.desiredNodes`
(plus creating/ready/draining/failed counts); and asks Forge Infrastructure to create or
drain/delete nodes by raising or lowering `NodePool.spec.replicas`. It never starts
containers or calls a cloud provider API.

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
| `FORGE_AUTOSCALER_EVAL_INTERVAL_MS` | `15000` | Evaluation tick interval (workload + node) |
| `FORGE_AUTOSCALER_METRIC_SOURCE` | `auto` | `auto` (Observe/Gateway/Queue/Runtime) or `fake` |
| `FORGE_OBSERVE_URL` | — | Prometheus-compatible query base for ObserveSource |
| `FORGE_RUNTIME_URL` | — | RuntimeSource local fallback |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Application/Worker/NodePool + placements/nodes APIs |
| `FORGE_GATEWAY_ADMIN_URL` | — | GatewaySource base (`GET /admin/metrics?application=`) |
| `FORGE_EVENTS_URL` | — | QueueSource + audit event publish base |
| `FORGE_AUTOSCALER_NODE_SCALE_UP_ENABLED` | `true` | Enable node scale loop (up + optional down) |
| `FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS` | `60` | Min gap between distinct scale-up windows |
| `FORGE_AUTOSCALER_NODE_SCALE_DOWN_ENABLED` | `true` | Enable node scale-down / drain path |
| `FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS` | `300` | Min gap / max-delete window for scale-down |
| `FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_THRESHOLD` | `0.25` | Max utilization to score a drain candidate |
| `FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS` | `300` | How long a node must stay underutilized |
| `FORGE_AUTOSCALER_NODE_MAX_DELETES_PER_WINDOW` | `1` | Cap deletes per cooldown window |
| `FORGE_AUTOSCALER_NODE_SCALE_DOWN_UNCORDON_ON_BLOCK` | `true` | Restore desiredNodes when drain is blocked |
| `FORGE_AUTOSCALER_RESERVATION_THRESHOLD` | `0.85` | Cluster reservation ratio that triggers proactive scale-up |
| `FORGE_AUTOSCALER_NODE_DEFAULT_MAX_NODES` | `100` | Fallback max when NodePool omits `scaling.maxNodes` |
| `FORGE_AUTH_MODE` | `dev` | Temporary until epic 09 hardening |

## API

* `POST/GET/PUT/PATCH/DELETE /v1/projects/{project}/environments/{environment}/scalingpolicies[/{name}]`
* `PUT .../scalingpolicies/{name}/status`
* `PUT/GET/DELETE .../scalingpolicies/{name}/override` — manual override subresource (TTL + reason)
* `GET /v1/watch/scalingpolicies?since={resourceVersion}` — SSE `ADDED` / `MODIFIED` / `STATUS_MODIFIED` / `DELETED`
* `GET /health/live`, `GET /health/ready`
* `GET /metrics` — workload metrics plus:
  * `forge_node_autoscaler_pending_workloads_total`
  * `forge_node_autoscaler_scale_up_requests_total{nodepool,result}`
  * `forge_node_autoscaler_scale_down_candidates_total`
  * `forge_node_autoscaler_drains_total{result}`

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

## Node scale-up (24.06)

```text
GET /v1/placements?status=pending  (cluster-wide)
GET /v1/nodes                      (reservation ratios)
→ select NodePool by priority + labels/region/arch/GPU + maxNodes
→ patch NodePool.spec.replicas + status.desiredNodes (idempotent operation id)
→ Infrastructure CreateNode (ledger) → Runtime registers → scheduler places pending
```

* No matching pool → condition / event `NoEligibleNodePool`
* Inventory exhausted → `ProviderCapacityBlocked`
* Duplicate ticks for the same pending demand window reuse `lastScaleUpOperationId`
* Cooldown applies between distinct demand windows

## Node scale-down (24.07)

```text
GET /v1/nodes                      (per-node utilization)
→ score underutilized Ready nodes over a configurable window
→ safeguards: minNodes, max deletes/window, cooldown, active rollout,
  disruption budget (25.04 soft), stateful primary (25.05 soft)
→ nominate drainCandidates + lower spec.replicas / status.desiredNodes
→ Infrastructure cordons (offline) + drains → deletes empty node (idempotent op id)
```

* Stateful primary workloads are never nominated (`StatefulPrimaryProtected`)
* Replacement capacity missing → `canceled` and desiredNodes restored (uncordon)
* Provider delete failure → `DeleteBlocked` with the same operation id retained
* Restart recovery resumes in-progress drains from NodePool status
* Events: `node.drain.started` / `node.drain.completed` plus `resource.nodepool.decided`
* Span: `autoscaler.node.scale_down`

## Tests

```bash
make -C services/forge-autoscaler test-unit
```

Postgres-backed store/HTTP tests skip when `forge_autoscaler` is unreachable.
