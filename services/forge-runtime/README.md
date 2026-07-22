# forge-runtime

Single-node container runtime for Forge Platform (Rust + Axum + bollard).

This service exposes Docker-backed readiness, a stable node identity that
persists across restarts, a periodic heartbeat, and workload create/start
(`POST/GET /v1/workloads`). Health probing, logs, and stop/delete arrive in
later steps (`04.04+`).

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
| `FORGE_CONTROL_URL` | _(unset)_ | Optional; registration stub logs intent when set (real register in 04.07). |

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

The node id is generated once (UUID) and stored at `$FORGE_RUNTIME_DATA_DIR/node_id`.
Compose mounts a named volume (`forge-runtime-data`) so the id survives container
restarts. Workload containers are labeled with this value as `forge.node_id`.

## Workloads

| Endpoint | Behavior |
|---|---|
| `POST /v1/workloads` | Pull image, create+start container with env/port/labels; `201` with `hostPort`. |
| `GET /v1/workloads/{deploymentId}` | Inspect by name `forge-<deploymentId>`; `404` if missing. |

Container name is deterministic (`forge-<deployment_id>`). Labels always include
`forge.deployment_id`, `forge.node_id`, and `forge.managed=true`. The container
port is published to an ephemeral host port; create returns `state: "starting"`
(health derivation is `04.04`). Env values are not logged — only keys.

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
Heartbeat healthy↔unhealthy transitions are logged. OTEL export is deferred.

## Security

* Health and node info endpoints are unauthenticated by design and expose only
  non-sensitive host facts (no env dumps, no secrets).
* Node id is non-secret but stable; the on-disk file is mode `0600`.
* Do not expose the Docker socket mount outside trusted local development.
