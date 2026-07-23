# Demo 24: Autoscaling gate (epic gate)

End-to-end acceptance gate for epic 24. Proves Forge Autoscaler scales
workloads, workers, and infrastructure nodes in local Docker mode with
provider-neutral resources and safety controls.

```text
Apply ScalingPolicy for invoice-api
  → generate HTTP load → replicas increase
  → stop load → replicas decrease after stabilization
  → publish queue backlog → workers increase and drain
  → deploy past cluster capacity
  → Node Autoscaler asks Infrastructure for another Docker runtime node
  → workload becomes Running
  → demand decreases → idle node drains and is deleted
  → manual override + metric-outage fallback visible in status
```

Compose builds are sequential (`COMPOSE_PARALLEL_LIMIT=1`). Metric injection uses
a tiny `demo24-metrics` sidecar that implements the Gateway/Events
`GET /admin/metrics` shapes the autoscaler already consumes (real Gateway/Events
admin aggregation is not required for this gate).

## Run

From the repository root:

```bash
make demo DEMO=24
```

Expect a final `demo 24 PASSED` line and exit code `0`.

## What this demo checks

* Workload replicas scale up/down from HTTP request-rate metrics
* Worker replicas scale up/down from a 20,000-job queue backlog signal
* Nodes scale up for unschedulable demand and scale down safely after drain
* Manual override and metric-outage fallback (`hold`) are visible on
  `ScalingPolicy.status`
* Autoscaler never starts containers or calls a cloud API — Control reconcile
  and Infrastructure own actuation

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API |
| `FORGE_AUTOSCALER_URL` | `http://127.0.0.1:4112` | Autoscaler API |
| `FORGE_INFRA_URL` | `http://127.0.0.1:4111` | Infrastructure health |
| `FORGE_DEMO24_METRICS_URL` | `http://127.0.0.1:4199` | Traffic/queue injector |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `COMPOSE_PARALLEL_LIMIT` | `1` | Sequential Compose builds |
| `FORGE_AUTOSCALER_EVAL_INTERVAL_MS` | `1000` | Fast eval for local gate |

## Fixtures

* `fixtures/nodepool-docker.yaml` — `InfrastructureProvider` + `NodePool(docker-pool)`
* `fixtures/scaling-policies.yaml` — Application + Worker `ScalingPolicy` specs
* `fixtures/application.yaml` — portable Project/Environment/Application/Service/Deployment

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Policy stuck at `minReplicas` under load | Metrics sidecar not reachable from autoscaler | Check `demo24-metrics` healthy; overlay sets `FORGE_GATEWAY_ADMIN_URL` / `FORGE_EVENTS_URL` |
| `metricOutageMode=hold` during Phase 1 | Load never published | Confirm `PUT /demo/application/invoice-api` returns 200 |
| Pending placements never clear | Node scale-up blocked / pool at `maxNodes` | Inspect `NodePool.status` and Infrastructure logs |
| Node scale-down timeout | Underutil window / sticky slot reservations / workloads still on victim | Ensure Application desiredReplicas synced to Deployment (demo sync loop). Control releases orphan placements with `replica_index >= desiredReplicas` each reconcile tick so CapacityReservation can shrink. |
| Override not visible | Stale `resourceVersion` | Re-GET policy before PUT/DELETE override |
| `make demo` `Killed: 9` mid-run | Host/Docker Desktop OOM (~8Gi) | Stop other stacks; Control overlay uses `-Xmx384m`; prefer sequential Compose |

Ops runbook: [`docs/operations/autoscaler-overrides.md`](../../docs/operations/autoscaler-overrides.md).  
Model: [`docs/concepts/autoscaling-model.md`](../../docs/concepts/autoscaling-model.md).

## Docs

* Epic: [`docs/implementation/epics/24-forge-autoscaler.md`](../../docs/implementation/epics/24-forge-autoscaler.md)
* Step: [`docs/implementation/steps/24-forge-autoscaler/24.08-demo-24-autoscaling.md`](../../docs/implementation/steps/24-forge-autoscaler/24.08-demo-24-autoscaling.md)
* Service: [`services/forge-autoscaler/README.md`](../../services/forge-autoscaler/README.md)
