# forge-gateway

HTTP edge gateway for Forge Platform (Go + `net/http`).

This step (`05.01`) delivers a bootable health-checked skeleton on host port
`4000`. Routing, reverse proxy, and Control sync arrive in later steps.

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-gateway
curl -sf http://127.0.0.1:4000/health/live
curl -sf http://127.0.0.1:4000/health/ready
```

Or from this directory:

```bash
make run
make test
```

Local binary (no Docker):

```bash
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4000:8080`. Required; invalid values fail startup. |
| `FORGE_SERVICE_NAME` | `forge-gateway` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Edge auth deferred to epic 09. |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Compose `stop_grace_period` should be ≥ this. |

Reserved for later steps (not read yet): `FORGE_CONTROL_URL`,
`FORGE_RUNTIME_URL`, `FORGE_ROUTE_SYNC_INTERVAL_SECONDS`.

## Health

| Path | Behavior |
|---|---|
| `GET /health/live` | `200` with `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` once the HTTP listener is accepting; `503` before that. |

Health endpoints are unauthenticated. No secrets are logged; startup logs
effective non-secret config as structured JSON (`timestamp`, `level`,
`service`, `message`).

## Security

* No edge authentication yet (`FORGE_AUTH_MODE=dev`).
* No routing or upstream proxy in this skeleton — only health surfaces.

## Development

```bash
make test-unit          # config + health unit tests
make test-integration   # Compose build/run, health, SIGTERM exit 0
make lint
make format
```
