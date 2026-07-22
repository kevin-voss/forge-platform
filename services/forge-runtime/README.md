# forge-runtime

Single-node container runtime for Forge Platform (Rust + Axum + bollard).

This step (`04.01`) delivers the service skeleton: env-based configuration,
structured JSON logs, graceful shutdown, Docker Engine connectivity over the
mounted socket, and health endpoints. Workload lifecycle APIs arrive in later
steps (`04.03+`).

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-runtime
curl -sf http://127.0.0.1:4102/health/live
curl -sf http://127.0.0.1:4102/health/ready
```

Or from this directory:

```bash
make run
make test
```

Local binary (uses the host Docker socket):

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

Invalid `PORT` causes a non-zero exit at startup.

## Health

| Endpoint | Behavior |
|---|---|
| `GET /health/live` | `200` `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` `{"status":"ok"}` only when `docker.ping()` succeeds; otherwise `503` `{"status":"not_ready"}`. |

If Docker is unreachable at startup, the process continues serving liveness after
bounded retries; readiness stays `503` until the Engine is reachable again.

## Docker socket (local dev)

Compose mounts `/var/run/docker.sock` into the `forge-runtime` container and runs
the process as `user: "0:0"` so the typical `root:root` mode-`660` socket is
reachable. This is a **privileged local-dev convenience** — the socket grants
broad host control and is flagged for later hardening (drop root, tighten
`group_add` / socket ownership).

## Observability

Structured JSON logs to stdout (`tracing` + JSON subscriber) include
`timestamp`, `level`, `service`, and `message`. Startup logs the Docker Engine
version when the ping succeeds. OTEL export is deferred.

## Security

* Health endpoints are unauthenticated by design.
* No secrets are logged; `FORGE_AUTH_MODE` is recorded at startup for auditability.
* Do not expose the Docker socket mount outside trusted local development.
