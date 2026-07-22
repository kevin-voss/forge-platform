# forge-build

Source-to-image build service for Forge Platform (Go + Docker Engine API).

Health-checked skeleton on host port `4103` with Docker socket access and an
isolated workspace volume (`06.01`). The `forge.yaml` schema, OpenAPI build-job
contract, and Go manifest/DTO validators are in place (`06.02`). Clone,
`docker build`, and registry push arrive in later `06.xx` steps.

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-build
curl -sf http://127.0.0.1:4103/health/live
curl -sf http://127.0.0.1:4103/health/ready
```

Or from this directory:

```bash
make run
make test
```

Local binary (uses the host Docker socket and a local workspace dir):

```bash
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4103:8080`. Required; invalid values fail startup. |
| `FORGE_SERVICE_NAME` | `forge-build` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Auth enforcement deferred to epic 09. |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. |
| `FORGE_BUILD_WORKSPACE_DIR` | `/workspace` | Absolute path for transient per-build clones. Must be writable. |
| `FORGE_DEFAULT_FORGE_YAML` | `forge.yaml` | Default relative path to the build manifest when the create request omits `forgeYamlPath`. |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Compose `stop_grace_period` should be ≥ this. |
| `FORGE_DOCKER_STARTUP_RETRIES` | `5` | Bounded ping retries at boot. |
| `FORGE_DOCKER_STARTUP_RETRY_DELAY_MS` | `500` | Delay between startup ping attempts. |

Invalid `PORT` or an unwritable workspace dir causes a non-zero exit at startup.

## Health

| Endpoint | Behavior |
|---|---|
| `GET /health/live` | `200` `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` `{"status":"ok"}` only when `docker.ping()` succeeds; otherwise `503` `{"status":"not_ready"}`. |

If Docker is unreachable at startup, the process continues serving liveness after
bounded retries; readiness stays `503` until the Engine is reachable again.

## Workspace

Each build gets an isolated directory under `FORGE_BUILD_WORKSPACE_DIR/<buildId>/`
(mode `0700`). The manager creates and fully removes these directories; no build
orchestration is wired yet (see `06.03`).

## Contracts

* `forge.yaml` JSON Schema: [`contracts/examples/forge.schema.json`](../../contracts/examples/forge.schema.json)
* Build OpenAPI: [`contracts/openapi/forge-build.openapi.yaml`](../../contracts/openapi/forge-build.openapi.yaml)
* Go parser: `internal/manifest` (path-traversal safe)
* DTOs + error envelope: `internal/api`

```text
POST /v1/builds              → 202 {buildId,status:queued}
GET  /v1/builds/{buildId}    → build status record
GET  /v1/builds/{buildId}/logs → streamed/plain logs
```

HTTP handlers that execute builds arrive in `06.03` / `06.05`; this step defines
the contract, fixtures, and validators.

## Docker socket

Compose mounts `/var/run/docker.sock` into the container. This is a **dev-only
privileged mount** (same model as `forge-runtime`) and grants broad host control;
harden before any non-local deployment.

## Observability

Structured JSON logs on stdout with `timestamp`, `level`, `service`, and
`message`. Startup logs the Docker Engine version and workspace directory.

## Development

```bash
make test-unit
make lint
make format
```
