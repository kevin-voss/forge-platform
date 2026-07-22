# forge-runtime

Single-node container runtime for Forge Platform (Rust + Axum + bollard).

This service exposes Docker-backed readiness, a stable node identity that
persists across restarts, and a periodic heartbeat. Workload lifecycle APIs
arrive in later steps (`04.03+`).

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-runtime
curl -sf http://127.0.0.1:4102/health/live
curl -sf http://127.0.0.1:4102/health/ready
curl -sf http://127.0.0.1:4102/v1/node | python3 -m json.tool
curl -sf http://127.0.0.1:4102/v1/node/heartbeat | python3 -m json.tool
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
restarts. The value is also exposed for later workload labeling as `forge.node_id`.

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
