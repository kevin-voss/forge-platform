# forge-events

Go HTTP service on host port `4105` that wires NATS JetStream and bootstraps
platform event streams (`build`, `deployment`, `runtime`, `application`, `agent`).

## Quick start

```bash
# From repo root (NATS must be up)
make -C services/forge-events run

curl -s localhost:4105/health/ready
curl -s localhost:4105/ | jq '{service,language,status}'
```

## Local development

```bash
make -C services/forge-events dev   # FORGE_NATS_URL defaults to nats://127.0.0.1:5002
make -C services/forge-events test
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4105` (`8080` in container) | Listen port |
| `FORGE_SERVICE_NAME` | `forge-events` | Identity + logs |
| `FORGE_LOG_LEVEL` | `info` | debug\|info\|warn\|error |
| `FORGE_NATS_URL` | `nats://nats:4222` | JetStream URL |
| `FORGE_EVENTS_STREAMS` | `build,deployment,runtime,application,agent` | Comma list |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Status

Step `11.01` — skeleton + JetStream wiring. Publish/subscribe lands in `11.02`.
