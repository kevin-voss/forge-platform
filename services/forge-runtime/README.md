# forge-runtime

Single-node container runtime for Forge Platform (Rust + Axum + bollard).

This service exposes Docker-backed readiness, a stable node identity that
persists across restarts, a periodic heartbeat, idempotent workload
create/start (`POST/GET /v1/workloads`), graceful stop/delete
(`DELETE /v1/workloads/{id}`), health probing with a normalized status model
(`GET /v1/workloads/{id}/status`), workload log fetch/follow
(`GET /v1/workloads/{id}/logs`), and Control desired→actual reconcile
(poll Control, converge containers, expose `GET /v1/node/state`, optionally
push status).

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-runtime
curl -sf http://127.0.0.1:4102/health/live
curl -sf http://127.0.0.1:4102/health/ready
curl -sf http://127.0.0.1:4102/v1/node | python3 -m json.tool
curl -sf http://127.0.0.1:4102/v1/node/heartbeat | python3 -m json.tool
curl -sf -X POST http://127.0.0.1:4102/v1/workloads -H 'content-type: application/json' -d '{
  "deployment_id":"deployment-123",
  "image":"localhost:5000/demo-go:latest",
  "port":8080,
  "environment":{"FORGE_ENV":"development","PORT":"8080"}
}'
# Wait for probes, then:
curl -sf http://127.0.0.1:4102/v1/workloads/deployment-123/status | python3 -m json.tool
curl -sf "http://127.0.0.1:4102/v1/workloads/deployment-123/logs?tail=20"
# follow (Ctrl-C to stop):
curl -N "http://127.0.0.1:4102/v1/workloads/deployment-123/logs?follow=true"
# idempotent retry (200, no duplicate container), then delete:
curl -sf -X POST http://127.0.0.1:4102/v1/workloads -H 'content-type: application/json' -d '{
  "deployment_id":"deployment-123",
  "image":"localhost:5000/demo-go:latest",
  "port":8080,
  "environment":{"FORGE_ENV":"development","PORT":"8080"}
}'
curl -sf -X DELETE http://127.0.0.1:4102/v1/workloads/deployment-123 -o /dev/null -w '%{http_code}\n'
```

Or from this directory:

```bash
make run
make test
```

Local binary (uses the host Docker socket and a local data dir):

```bash
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4102:8080`. |
| `FORGE_SERVICE_NAME` | `forge-runtime` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Auth enforcement deferred to epic 09. |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Compose `stop_grace_period` should be ≥ this. |
| `FORGE_DOCKER_STARTUP_RETRIES` | `5` | Bounded ping retries at boot. |
| `FORGE_DOCKER_STARTUP_RETRY_DELAY_MS` | `500` | Delay between startup ping attempts. |
| `FORGE_RUNTIME_DATA_DIR` | `/var/lib/forge-runtime` | Persists `node_id` (mode `0600`). Must be writable or startup fails. |
| `FORGE_HEARTBEAT_INTERVAL_SECONDS` | `10` | Periodic liveness tick interval. |
| `FORGE_PULL_TIMEOUT_SECONDS` | `120` | Max wait for `docker pull` during workload create. |
| `FORGE_DEFAULT_REGISTRY` | `localhost:5000` | Informational; workload images are fully qualified. |
| `FORGE_PROBE_INTERVAL_SECONDS` | `5` | Workload health probe interval. |
| `FORGE_PROBE_TIMEOUT_SECONDS` | `2` | Per-probe HTTP timeout. |
| `FORGE_PROBE_FAILURE_THRESHOLD` | `3` | Consecutive live failures before `unhealthy`. |
| `FORGE_PROBE_READY_PATH` | `/health/ready` | Readiness path on the workload. |
| `FORGE_PROBE_LIVE_PATH` | `/health/live` | Liveness path on the workload. |
| `FORGE_PROBE_HOST` | `127.0.0.1` | Host paired with published ports when container IP is unavailable. Compose sets `host.docker.internal`. |
| `FORGE_LOG_DEFAULT_TAIL` | `100` | Default `tail` for workload log fetch when omitted. |
| `FORGE_LOG_STREAM_BUFFER` | `8192` | Soft buffer (bytes) used to size follow-stream backpressure. |
| `FORGE_STOP_GRACE_SECONDS` | `10` | SIGTERM→SIGKILL grace when stopping a workload container. |
| `FORGE_ON_CONFIG_CONFLICT` | `recreate` | `recreate` replaces a conflicting image; `reject` returns `409`. |
| `FORGE_CONTROL_URL` | _(unset)_ locally / `http://forge-control:8080` in Compose | When set, starts the desired→actual reconcile loop. |
| `FORGE_LIFECYCLE_OWNER` | `runtime` locally / `control` in Compose | `runtime` create/stops locally; `control` delegates lifecycle to Control (epic 07). |
| `FORGE_RECONCILE_INTERVAL_SECONDS` | `10` | Poll/converge interval. |
| `FORGE_CONTROL_REPORT_MODE` | `push` | `push` POSTs status to Control (404 tolerated); `pull` relies on `GET /v1/node/state`. |

Invalid `PORT` or an unwritable data dir causes a non-zero exit at startup.

## Health

| Endpoint | Behavior |
|---|---|
| `GET /health/live` | `200` `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` `{"status":"ok"}` only when `docker.ping()` succeeds; otherwise `503` `{"status":"not_ready"}`. |

If Docker is unreachable at startup, the process continues serving liveness after
bounded retries; readiness stays `503` until the Engine is reachable again.

## Node identity

| Endpoint | Behavior |
|---|---|
| `GET /v1/node` | Stable node id, hostname, Docker version, best-effort cpu/memory, `startedAt`, `lastHeartbeat`. |
| `GET /v1/node/heartbeat` | `{ "nodeId", "at", "healthy" }` reflecting the latest liveness tick. |
| `GET /v1/node/state` | Actual workloads on this node: `{ nodeId, workloads:[{deploymentId,status,hostPort,image}] }` (Control pull interim). |

The node id is generated once (UUID) and stored at `$FORGE_RUNTIME_DATA_DIR/node_id`.
Compose mounts a named volume (`forge-runtime-data`) so the id survives container
restarts. Workload containers are labeled with this value as `forge.node_id`.

## Control integration

When `FORGE_CONTROL_URL` is set, Runtime polls Control each reconcile interval:

1. Prefer `GET /v1/deployments?nodeId=&desired=true` (documented contract).
2. If that endpoint is missing (404), walk `GET /v1/projects` + `?expand=tree` and treat every deployment with `desiredReplicas > 0` as desired for this single node.
3. Converge: idempotent ensure for desired ids; delete managed containers not in the desired set.
4. Report: with `FORGE_CONTROL_REPORT_MODE=push`, `POST /v1/deployments/{id}/status` with `{status,nodeId,endpoint:{hostPort}}` (Control implements this as of demo `04.08`; a 404 is still tolerated and `/v1/node/state` remains available for pull).

Runtime status maps to Control as: `ready`/`running`→`active`, `failed`/`unhealthy`→`failed`, `stopped`→`stopped`, `starting`→`pending`.

If Control is unreachable, the cycle is skipped (warning logged) with no local churn.

## Workloads

| Endpoint | Behavior |
|---|---|
| `POST /v1/workloads` | Idempotent ensure: `201` when created, `200` when an existing managed container is reused/restarted. Conflicting image → recreate (default) or `409`. |
| `GET /v1/workloads/{deploymentId}` | Inspect by name `forge-<deploymentId>`; `404` if missing. |
| `DELETE /v1/workloads/{deploymentId}` | Marks unready, then graceful stop (SIGTERM + grace) and remove; `204` (idempotent when already gone). Only `forge.managed=true` containers are touched. |
| `POST /v1/workloads/{deploymentId}/drain` | Marks status `stopped` without removing the container so Gateway can drop the upstream before stop (rolling updates). |
| `GET /v1/workloads/{deploymentId}/status` | Normalized status + last probe details. |
| `GET /v1/workloads/{deploymentId}/logs` | Bounded log fetch (`tail`, `since`, `streams`) or SSE follow (`follow=true`). |

Container name is deterministic (`forge-<deployment_id>`). Labels always include
`forge.deployment_id`, `forge.node_id`, and `forge.managed=true`. The container
port is published to an ephemeral host port. Env values are not logged — only keys.
Concurrent creates for the same deployment id are serialized by an in-process lock.

### Logs

| Query | Default | Notes |
|---|---|---|
| `tail` | `FORGE_LOG_DEFAULT_TAIL` | Last N lines (`all` allowed). |
| `since` | _(none)_ | RFC3339 lower bound. |
| `streams` | `all` | `stdout` \| `stderr` \| `all`. |
| `follow` | `false` | When `true`, responds with `text/event-stream`. |
| `format` | `text` | `text` → `text/plain` with `[stdout]`/`[stderr]` prefixes; `ndjson` → `application/x-ndjson`. |

Only managed workloads (`forge.managed=true`) are readable; unknown ids return `404`.
Client disconnect cancels the underlying Docker log stream. Log aggregation is
deferred to epic 12; authz for log access lands in epic 09.

## Status model

A background prober rediscovers managed containers (label `forge.managed=true`)
on startup and periodically probes `/health/live` and `/health/ready`. Status is
derived from Docker state + probe results:

| Status | Meaning |
|---|---|
| `starting` | Created/warming; live not yet confirmed within the failure threshold. |
| `running` | Container running and live `200`, but ready not yet `200`. |
| `ready` | Container running and `/health/ready` returned `200`. |
| `unhealthy` | Running but live failed for ≥ `FORGE_PROBE_FAILURE_THRESHOLD` cycles. |
| `failed` | Container exited/dead unexpectedly. |
| `stopped` | Operator-initiated stop (04.06) or paused/removing. |

Example:

```json
{
  "deploymentId": "deployment-123",
  "status": "ready",
  "since": "2026-07-22T17:00:00Z",
  "lastProbe": { "live": true, "ready": true, "at": "2026-07-22T17:00:05Z" },
  "restarts": 0
}
```

Probes prefer the container network IP + container port when available, and fall
back to `FORGE_PROBE_HOST` + published host port. Probe response bodies are not
logged — only success/failure.

## Docker socket (local dev)

Compose mounts `/var/run/docker.sock` into the `forge-runtime` container and runs
the process as `user: "0:0"` so the typical `root:root` mode-`660` socket is
reachable. This is a **privileged local-dev convenience** — the socket grants
broad host control and is flagged for later hardening (drop root, tighten
`group_add` / socket ownership).

## Observability

Structured JSON logs to stdout (`tracing` + JSON subscriber) include
`timestamp`, `level`, `service`, and `message`. Startup logs whether the node id
was generated or loaded, and the Docker Engine version when the ping succeeds.
Heartbeat healthy↔unhealthy transitions and workload status transitions
(old→new + probe fields) are logged. OTEL export is deferred.

## Security

* Health and node info endpoints are unauthenticated by design and expose only
  non-sensitive host facts (no env dumps, no secrets).
* Node id is non-secret but stable; the on-disk file is mode `0600`.
* Probes hit only managed workloads (container IP or mapped host port).
* Do not expose the Docker socket mount outside trusted local development.
