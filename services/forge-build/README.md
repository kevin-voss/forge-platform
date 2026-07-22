# forge-build

Source-to-image build service for Forge Platform (Go + Docker Engine API).

Health-checked service on host port `4103` with Docker socket access and an
isolated workspace volume. Accepts build jobs (`POST /v1/builds`), clones a
local/`file://` Git ref into `workspace/<buildId>`, validates `forge.yaml`, runs
`docker build` with a timeout, tags/pushes the image to the local OCI registry
(`localhost:5000`), persists durable build status, and streams logs via
`GET /v1/builds/{id}/logs`.

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

## Build a fixture

```bash
make prepare-fixture
make run
BID=$(curl -sf -X POST http://127.0.0.1:4103/v1/builds -H 'content-type: application/json' \
  -d '{"repo":"file:///fixtures/app","ref":"main","forgeYamlPath":"forge.yaml","project":"acme"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["buildId"])')
curl -N "http://127.0.0.1:4103/v1/builds/$BID/logs?follow=true"
curl -sf "http://127.0.0.1:4103/v1/builds/$BID" | python3 -m json.tool
curl -sf 'http://127.0.0.1:4103/v1/builds?status=succeeded' | python3 -m json.tool
IMG=$(curl -sf "http://127.0.0.1:4103/v1/builds/$BID" | python3 -c 'import sys,json;print(json.load(sys.stdin)["image"])')
echo "$IMG"
curl -sf http://localhost:5000/v2/_catalog
docker pull "$IMG"
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
| `FORGE_BUILD_STORE_DIR` | `/var/lib/forge-build` | Durable JSON build records (survives restart). |
| `FORGE_BUILD_RETENTION_HOURS` | `72` | Terminal records older than this are pruned on startup. |
| `FORGE_BUILD_CLEANUP_ON_START` | `true` | Sweep leftover workspace dirs on startup. |
| `FORGE_DEFAULT_FORGE_YAML` | `forge.yaml` | Default relative path to the build manifest when the create request omits `forgeYamlPath`. |
| `FORGE_BUILD_TIMEOUT_SECONDS` | `600` | Cancels clone/build/push when exceeded; build marked `failed`. |
| `FORGE_BUILD_MAX_CONCURRENCY` | `2` | Bounded worker pool; additional jobs wait in queue. |
| `FORGE_BUILD_LOG_BUFFER_LINES` | `5000` | Retained log lines per build (ring buffer). |
| `FORGE_REGISTRY` | `localhost:5000` | Local OCI registry host:port (no scheme). |
| `FORGE_IMAGE_NAME_PATTERN` | `{project}-{service}` | Repository name template; `{service}` required. Empty project collapses hyphens. |
| `FORGE_DEFAULT_PROJECT` | _(empty)_ | Used when the create request omits `project`. |
| `FORGE_PUSH_LATEST` | `true` | Also tag/push a moving `:latest`. |
| `FORGE_PUSH_RETRIES` | `3` | Additional push attempts after the first failure. |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Compose `stop_grace_period` should be ≥ this. |
| `FORGE_DOCKER_STARTUP_RETRIES` | `5` | Bounded ping retries at boot. |
| `FORGE_DOCKER_STARTUP_RETRY_DELAY_MS` | `500` | Delay between startup ping attempts. |

Invalid `PORT` or an unwritable workspace dir causes a non-zero exit at startup.

## Health

| Endpoint | Behavior |
|---|---|
| `GET /health/live` | `200` `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` `{"status":"ok"}` only when `docker.ping()` succeeds; otherwise `503` `{"status":"not_ready"}`. |

## Build API

| Endpoint | Behavior |
|---|---|
| `POST /v1/builds` | Validate body, enqueue job, `202` `{buildId,status:queued}`. |
| `GET /v1/builds` | List builds; optional `?status=` and `?service=` filters. |
| `GET /v1/builds/{buildId}` | Status/phase, timestamps, commit, image/digest (success only), structured error. |
| `POST /v1/builds/{buildId}/cancel` | `202` `{status:canceling}` for queued/running; `409` if already terminal. |
| `GET /v1/builds/{buildId}/logs` | `text/plain` logs; `?follow=true` streams until the build finishes. |

Status machine: `queued → running(cloning|building|pushing) → succeeded|failed`, or `canceled` from queued/running.

Invariant: `image` is present if and only if `status == succeeded`. Failed/canceled builds never expose a deployable image.

Only local absolute paths and `file://` repos are accepted in this epic. Remote Git URLs are rejected.

On success the image is tagged as
`localhost:5000/<project>-<service>:<shortSha>-<buildId>` (plus `:latest` when
enabled), pushed to the local registry, and recorded on the build with its
content digest. Each build uses an isolated workspace directory that is removed
on every terminal path (success, failure, cancel). On restart, orphaned
`queued`/`running` builds are marked `failed` (`interrupted`) and workspaces are swept.

## Contracts

* `forge.yaml` JSON Schema: [`contracts/examples/forge.schema.json`](../../contracts/examples/forge.schema.json)
* Build OpenAPI: [`contracts/openapi/forge-build.openapi.yaml`](../../contracts/openapi/forge-build.openapi.yaml)
* Go parser: `internal/manifest`
* Job engine: `internal/git`, `internal/builder`, `internal/registry`, `internal/jobs`, `internal/store`, `internal/logbuf`

## Docker socket

Compose mounts `/var/run/docker.sock` into the container. This is a **dev-only
privileged mount** (same model as `forge-runtime`) and grants broad host control;
harden before any non-local deployment.

## Observability

Structured JSON logs on stdout with `timestamp`, `level`, `service`, and
`message`. Build lifecycle logs include phase transitions with `build_id` and
phase duration, plus startup orphan sweeps and workspace cleanup.

## Development

```bash
make test-unit
make lint
make format
```
