# forge-control

Central control-plane API for Forge Platform (Kotlin + Ktor).

Persistence (`02.02`) provides the Control domain model — projects, environments,
applications, services, deployments, and an append-only audit log — in PostgreSQL
schema `control`, with Flyway migrations, HikariCP, and JDBC repositories.

HTTP APIs for projects, environments, applications, services, and desired-state
deployments (`02.05`) are available under `/v1`. Control records deployment
intent only; it does not pull images or start containers.

A reconciliation controller (`07.01`–`07.05`) periodically diffs desired vs
actual replica state, converges via Runtime, performs rolling updates, and on
rollout timeout/failure automatically rolls back to the last healthy version.
Status is exposed at `GET /v1/deployments/{id}/reconcile`; transition history at
`GET /v1/deployments/{id}/history`. On restart the controller adopts existing
workloads and resumes in-flight rollouts/rollbacks without duplicating containers.

A scheduler module (`08.01`) decides replica placement via
`Scheduler.place(PlacementRequest) → PlacementDecision` (default
`SingleNodeScheduler` → `node-local`). Placements persist uniquely per
`(deployment_id, replica_index)` and are exposed at `POST/GET /v1/placements`.
The reconciler records a placement before starting each replica. The module has
a documented extract seam for a future standalone service on port `4108`.

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-control
curl -sf http://127.0.0.1:4001/health/live
curl -sf http://127.0.0.1:4001/health/ready
```

Create a project and environment:

```bash
PID=$(curl -sf -X POST http://127.0.0.1:4001/v1/projects \
  -H 'content-type: application/json' -d '{"name":"acme"}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf http://127.0.0.1:4001/v1/projects/$PID
EID=$(curl -sf -X POST http://127.0.0.1:4001/v1/projects/$PID/environments \
  -H 'content-type: application/json' -d '{"name":"development"}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf http://127.0.0.1:4001/v1/projects/$PID/environments
```

Create an application and service:

```bash
AID=$(curl -sf -X POST http://127.0.0.1:4001/v1/projects/$PID/applications \
  -H 'content-type: application/json' -d '{"name":"web"}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
SID=$(curl -sf -X POST http://127.0.0.1:4001/v1/applications/$AID/services \
  -H 'content-type: application/json' -d '{"name":"api","port":8080}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf http://127.0.0.1:4001/v1/applications/$AID/services
```

Create and inspect a desired deployment:

```bash
DID=$(curl -sf -X POST http://127.0.0.1:4001/v1/services/$SID/deployments \
  -H 'content-type: application/json' \
  -d "{\"image\":\"localhost:5000/demo-go:latest\",\"desiredReplicas\":1,\"environmentId\":\"$EID\"}" | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf http://127.0.0.1:4001/v1/deployments/$DID
curl -sf "http://127.0.0.1:4001/v1/projects/$PID?expand=tree"
```

Or from this directory:

```bash
make migrate   # apply schema without starting HTTP
make run
make test
```

Local JVM (no Docker for the service; needs foundation Postgres on `:5001`):

```bash
make migrate
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4001:8080`. Wins over `FORGE_HTTP_PORT`. |
| `FORGE_SERVICE_NAME` | `forge-control` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_OTEL_ENABLED` | `true` | Set `false` for hermetic tests; keeps no-op tracing and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://otel-collector:4317` | OTLP/gRPC Collector endpoint. |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Auth bypass until Identity `09.06`; creates attributed to actor `dev` |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |
| `DATABASE_URL` | `jdbc:postgresql://127.0.0.1:5001/forge` (local) / `…://postgres:5432/forge` (Compose) | JDBC URL |
| `DATABASE_USER` | `forge` | |
| `DATABASE_PASSWORD` | `forge` | Never logged |
| `DATABASE_SCHEMA` | `control` | Flyway default schema |
| `DATABASE_POOL_MAX` | `10` | HikariCP max pool size |
| `DATABASE_MIGRATE_ON_START` | `true` | Run Flyway on boot |
| `FORGE_IDEMPOTENCY_TTL_HOURS` | `24` | Retention target for idempotency records; cleanup is deferred |
| `FORGE_RECONCILE_ENABLED` | `true` | Master switch for the reconcile controller loop |
| `FORGE_RECONCILE_INTERVAL_MS` | `2000` | Controller tick interval |
| `FORGE_RECONCILE_MAX_ACTIONS_PER_TICK` | `5` | Max start/stop/rollout actions applied per deployment per tick |
| `FORGE_RUNTIME_URL` | `http://forge-runtime:4102` | Fallback Runtime URL for observe/stop; creates route via placement node address (`PlacementAwareRuntimeClient`) |
| `FORGE_GATEWAY_URL` | `http://forge-gateway:4000` | Base URL for Gateway admin refresh during rolling traffic shift |
| `FORGE_ROLLOUT_BATCH_SIZE` | _(unset)_ | When set, overrides per-deployment `rollout_batch_size` |
| `FORGE_ROLLOUT_TIMEOUT_S` | _(unset)_ | When set, overrides per-deployment `rollout_timeout_s` (default 120) |
| `FORGE_ROLLBACK_ENABLED` | `true` | Automatic rollback to last healthy on rollout timeout/failure |
| `FORGE_READINESS_POLL_MS` | `1000` | Readiness poll interval for rolling updates |
| `FORGE_READINESS_MAX_WAIT_S` | `60` | Max wait for a new replica to become ready before holding the rollout |
| `FORGE_HISTORY_ENABLED` | `true` | Append status transitions to `deployment_events` |
| `FORGE_STARTUP_ADOPT_LABELS` | `true` | On boot, adopt existing workloads and GC orphans before reconcile |
| `FORGE_SCHEDULER_ENABLED` | `true` | Reconciler delegates placement before starting replicas |
| `FORGE_SCHEDULER_STRATEGY` | `least-allocated` | `first-fit` \| `least-allocated` \| `single-node` |
| `FORGE_SCHEDULER_LOCAL_NODE_ID` | `node-local` | Fallback sole node for `single-node` when the fleet is empty |
| `FORGE_RESCHEDULE_ENABLED` | `true` | On node offline, mark placements lost and request replacements (`08.05`) |
| `FORGE_RESCHEDULE_GRACE_S` | `5` | Wait after offline before rescheduling (suppresses fast flaps) |

See `.env.example`.

## Observability

Control writes JSON lines to stdout with `timestamp`, `level`, `service`,
`message`, and `requestId`. Request logs generated while a trace is active also
include matching `traceId` and `spanId`. With OTEL enabled, HTTP request and JDBC
repository spans plus request count, duration, and error metrics are exported to
the foundation Collector. Reconcile ticks emit `forge_reconcile_ticks_total` /
`forge_reconcile_plan_actions`, executed-action counter
`forge_reconcile_actions_total{action=start|stop|recreate|…}`,
`forge_rollout_step_total{step=…}`,
`forge_rollout_result_total{result=deployed|rolled_back}`,
`forge_rollback_duration_ms`,
`forge_deployment_transitions_total{to_status=…}`, and spans
`reconcile.tick` / `reconcile.rolling_update` / `reconcile.rollback` /
`reconcile.start_replica` / `reconcile.wait_ready` /
`reconcile.shift_traffic` / `reconcile.drain_replica` /
`reconcile.stop_replica`, plus `forge_placements_total{strategy=…}`,
`forge_placement_decisions_total{strategy,node}`,
`forge_placement_rejected_no_capacity_total`,
`forge_reschedule_total{result=placed|pending}`,
`forge_node_offline_total`,
`forge_stale_replicas_fenced_total`, and spans `scheduler.place` /
`scheduler.reschedule` (attributes `strategy`, `candidates`, `node`).
From 07.02 the controller executes start/stop/recreate against Runtime using
deterministic per-replica workload ids
(`forge-<service_slug>-<deployment_short>-<index>`). From 07.03 image changes
roll one batch at a time (start → ready → Gateway shift → drain → stop old)
while keeping at least `desired - batch_size` ready replicas. From 07.04 a
rollout that does not reach readiness within `rollout_timeout_s` rolls back to
the last healthy image/replica count and sets status `rolled_back`. From 07.05
every lifecycle transition is appended to `deployment_events` atomically with
the status update (`GET /v1/deployments/{id}/history`), and on Control restart
`StartupRecovery` adopts existing labeled workloads and GCs orphans before the
reconcile loop resumes. Exporter failures are asynchronous and do not stop
request handling.

## HTTP API (02.05)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/v1/projects` | Body `{"name","slug?"}`; slug derived from name when omitted |
| `GET` | `/v1/projects` | List projects |
| `GET` | `/v1/projects/{projectId}` | Get project |
| `POST` | `/v1/projects/{projectId}/environments` | Body `{"name"}` |
| `GET` | `/v1/projects/{projectId}/environments` | List environments for project |
| `GET` | `/v1/environments/{environmentId}` | Get environment |
| `POST` | `/v1/projects/{projectId}/applications` | Body `{"name"}` |
| `GET` | `/v1/projects/{projectId}/applications` | List applications for project |
| `GET` | `/v1/applications/{applicationId}` | Get application |
| `POST` | `/v1/applications/{applicationId}/services` | Body `{"name","port"}`; port is 1–65535 |
| `GET` | `/v1/applications/{applicationId}/services` | List services for application |
| `GET` | `/v1/services/{serviceId}` | Get service (includes optional `image` / `imageDigest` / `imageCommit` / `imageBuildId` when recorded) |
| `POST` | `/v1/services/{serviceId}/image` | Record built image on service; body `{"image","digest?","commit?","buildId?"}`; idempotent via `Idempotency-Key` |
| `POST` | `/v1/services/{serviceId}/deployments` | Body `{"image","desiredReplicas?","environmentId"}`; replicas default to `1`, status starts `pending` |
| `GET` | `/v1/services/{serviceId}/deployments` | List deployments for a service |
| `GET` | `/v1/deployments/{deploymentId}` | Get deployment |
| `POST` | `/v1/deployments/{deploymentId}/status` | Runtime actual-state report: `{"status","nodeId","endpoint":{"hostPort"?}}` → `pending`/`active`/`failed`/`stopped` |
| `DELETE` | `/v1/deployments/{deploymentId}` | Remove desired deployment (`204`); Runtime orphan cleanup removes the container |
| `PATCH` | `/v1/deployments/{deploymentId}` | Update desired `image` and/or `desiredReplicas` (triggers rolling update / rollback paths) |
| `GET` | `/v1/deployments/{deploymentId}/reconcile` | Desired/actual snapshot, plan, `phase`, `updatedReplicas`, `currentImage`/`targetImage`, `status` (`deploying`/`deployed`/`rolling_back`/`rolled_back`/…), `lastHealthyImage`, controller health (`07.01`–`07.04`) |
| `GET` | `/v1/deployments/{deploymentId}/history` | Chronological append-only transition trail (`07.05`) |
| `POST` | `/v1/placements` | Compute+persist placement; body `{"deployment_id","replica_index","requirements?","anti_affinity?","service_id?"}`; soft/hard anti-affinity; `201` placed, `202` pending when no capacity (`08.04`); `409 queue_full` at cap |
| `GET` | `/v1/placements?deployment=&status=` | List placements for a deployment; optional `status=placed|pending|lost` (`08.04`/`08.05`) |
| `GET` | `/v1/projects/{projectId}?expand=tree` | Project, environments, applications, services, and deployments |

The machine-readable API contract is
[`contracts/openapi/forge-control.openapi.yaml`](../../contracts/openapi/forge-control.openapi.yaml).
All errors use `{"error":{"code","message","details?","requestId"}}` with consistent
`400 validation_error`, `404 not_found`, `409 conflict`, and `500 internal_error` codes.
Every response carries `X-Request-Id`. All POST creates accept an optional
`Idempotency-Key`; the same key and body replay the original response, while a changed
body returns `409 idempotency_key_conflict`.

## Schema

Tables in schema `control`:

* `projects`, `environments`, `applications`, `services`, `deployments`
* `reconcile_status` (per-deployment desired/actual/plan snapshot, lifecycle status, rollout timer, controller health)
* `deployment_events` (append-only status transitions with image, replica counts, reason)
* `placements` (replica → node assignments, `pending` queue, or `lost` audit rows; active unique on `(deployment_id, replica_index)` where `status in (placed,pending)`; `rescheduled_from_node` on replacements)
* `audit_log` (append-only; create actions for projects/environments/applications/services/deployments)
* `idempotency_keys` (key, request hash, resource ID, stored response; 24-hour retention target)
* `flyway_schema_history`

`deployments` also stores rollout policy defaults (`rollout_batch_size=1`,
`rollout_timeout_s=120`) and lifecycle status (`pending`/`deploying`/`deployed`/
`rolling_back`/`rolled_back`, plus Runtime `active`/`failed`/`stopped`).
`services` stores `last_healthy_deployment_id` / `last_healthy_image` /
`last_healthy_replicas` for rollback. Foreign keys use `ON DELETE RESTRICT`
(reconcile snapshots cascade on deployment delete). Unique constraints enforce
slug/name uniqueness within parents. Check constraints cover non-blank names,
`port` 1–65535, and `desired_replicas >= 0`.

## Health

* `GET /health/live` → `200 {"status":"live"}`
* `GET /health/ready` → `200 {"status":"ready"}` once started **and** `SELECT 1` succeeds; `503` otherwise

## Makefile targets

```bash
make migrate        # Flyway only (no HTTP server)
make dev            # run with Gradle on localhost
make build          # jar
make run            # Compose up on :4001
make test           # unit + integration
make test-unit
make test-integration
make docker-build
make clean
```
