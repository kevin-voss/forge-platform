# forge-events

Go HTTP service on host port `4105` that wraps NATS JetStream with a Forge
publish/consume API. Bootstraps platform event streams (`build`, `deployment`,
`runtime`, `application`, `agent`) plus per-family dead-letter streams
(`dlq_<family>`), and exposes durable consumers with explicit ack/nak, bounded
retry, and DLQ inspect/redeliver APIs.

## Quick start

```bash
# From repo root (NATS must be up)
make -C services/forge-events run

curl -s localhost:4105/health/ready
curl -s localhost:4105/ | jq '{service,language,status}'

curl -s -X POST localhost:4105/v1/consumers \
  -H 'content-type: application/json' \
  -d '{"name":"crash-worker","subject":"application.crashed","ack_wait_s":30,"max_deliveries":5}'

curl -s -X POST localhost:4105/v1/events \
  -H 'content-type: application/json' \
  -d '{"subject":"application.crashed","data":{"service":"demo","reason":"oom"},"source":"runtime"}'

curl -s -X POST localhost:4105/v1/consume \
  -H 'content-type: application/json' \
  -d '{"consumer":"crash-worker","batch":10}' | jq '.messages[0] | {subject, delivery_count, ack_token}'

# Acknowledge (or nak) using the opaque token
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:4105/v1/ack \
  -H 'content-type: application/json' \
  -d '{"ack_token":"<token>"}'

# After max_deliveries naks, inspect the DLQ
curl -s 'localhost:4105/v1/dlq?subject=application.crashed' | jq
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
| `FORGE_DEFAULT_ACK_WAIT_S` | `30` | Default ack wait / redelivery delay |
| `FORGE_DEFAULT_MAX_DELIVERIES` | `5` | Default max delivery attempts |
| `FORGE_ACK_TOKEN_TTL_S` | `60` (or ≥ ack wait) | Opaque ack token validity window |
| `FORGE_DLQ_ENABLED` | `true` | Bootstrap `dlq_*` streams + route terminal failures |
| `FORGE_DLQ_RETENTION_DAYS` | `7` | Age-based DLQ index/stream cleanup |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## DLQ

Messages that exhaust `max_deliveries` are published to `dlq.<family>.entry`
with failure metadata headers (`original_subject`, `consumer`, `delivery_count`,
`last_error`, `first_failed_at`). Operators can:

* `GET /v1/dlq?subject=&consumer=` — list
* `GET /v1/dlq/{dlq_id}` — full envelope + metadata
* `POST /v1/dlq/{dlq_id}:redeliver` — republish to the original subject (same event id)
* `DELETE /v1/dlq/{dlq_id}` — acknowledge/remove

DLQ routing never silently drops a poison message: publish failures are queued
for retry.

## Auth

No auth on publish/consume/DLQ yet. Identity tokens land in step `11.06`.

## Status

Step `11.04` — dead-letter queue with inspect and redeliver APIs. Schemas are `11.05`.
