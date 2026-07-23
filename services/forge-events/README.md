# forge-events

Go HTTP service on host port `4105` that wraps NATS JetStream with a Forge
publish/consume API. Bootstraps platform event streams (`build`, `deployment`,
`runtime`, `application`, `agent`) and exposes `POST /v1/events` +
`POST /v1/consume`.

## Quick start

```bash
# From repo root (NATS must be up)
make -C services/forge-events run

curl -s localhost:4105/health/ready
curl -s localhost:4105/ | jq '{service,language,status}'

curl -s -X POST localhost:4105/v1/events \
  -H 'content-type: application/json' \
  -d '{"subject":"application.crashed","data":{"service":"demo","reason":"oom"},"source":"runtime"}'

curl -s -X POST localhost:4105/v1/consume \
  -H 'content-type: application/json' \
  -d '{"subject":"application.crashed","batch":10}' | jq '.messages[0] | {subject, data}'
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
| `FORGE_EVENTS_STREAMS` | `build,deployment,runtime,application,agent` | Comma list / subject families |
| `FORGE_EVENT_MAX_BYTES` | `262144` (256KB) | Max event/envelope bytes; oversize → 413 |
| `FORGE_CONSUME_MAX_BATCH` | `100` | Cap for `POST /v1/consume` batch |
| `FORGE_CONSUME_WAIT_MS` | `2000` | Long-poll wait for empty pull |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Auth

No auth on publish/consume yet. Identity tokens land in step `11.06`.

## Status

Step `11.02` — publish + pull-consume over JetStream. Durable ack/retry is `11.03`.
