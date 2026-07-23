# forge-events

Go HTTP service on host port `4105` that wraps NATS JetStream with a Forge
publish/consume API. Bootstraps platform event streams (`build`, `deployment`,
`runtime`, `application`, `agent`) plus per-family dead-letter streams
(`dlq_<family>`), validates publish payloads against JSON Schemas under
`contracts/events/`, and exposes durable consumers with explicit ack/nak,
bounded retry, and DLQ inspect/redeliver APIs.

## Quick start

```bash
# From repo root (NATS must be up)
make -C services/forge-events run

curl -s localhost:4105/health/ready
curl -s localhost:4105/v1/schemas | jq 'keys'

curl -s -X POST localhost:4105/v1/consumers \
  -H 'content-type: application/json' \
  -d '{"name":"crash-worker","subject":"application.crashed","ack_wait_s":30,"max_deliveries":5}'

curl -s -X POST localhost:4105/v1/events \
  -H 'content-type: application/json' \
  -d '{"subject":"application.crashed","data":{"service":"demo","reason":"oom","occurred_at":"2026-07-22T14:00:00Z"},"source":"runtime"}'

# Malformed (missing required fields) → 422
curl -s -X POST localhost:4105/v1/events \
  -H 'content-type: application/json' \
  -d '{"subject":"application.crashed","data":{"reason":"oom"},"source":"runtime"}' | jq '{error, violations}'

curl -s -X POST localhost:4105/v1/consume \
  -H 'content-type: application/json' \
  -d '{"consumer":"crash-worker","batch":10}' | jq '.messages[0] | {subject, delivery_count, ack_token}'
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
| `FORGE_EVENT_SCHEMA_DIR` | `/contracts/events` | JSON Schema directory (Draft 2020-12) |
| `FORGE_SCHEMA_VALIDATION` | `strict` | `strict` rejects invalid/unknown; `warn` logs and allows |
| `FORGE_CONSUME_MAX_BATCH` | `100` | Cap for `POST /v1/consume` batch |
| `FORGE_CONSUME_WAIT_MS` | `2000` | Long-poll wait for empty pull |
| `FORGE_DEFAULT_ACK_WAIT_S` | `30` | Default ack wait / redelivery delay |
| `FORGE_DEFAULT_MAX_DELIVERIES` | `5` | Default max delivery attempts |
| `FORGE_ACK_TOKEN_TTL_S` | `60` (or ≥ ack wait) | Opaque ack token validity window |
| `FORGE_DLQ_ENABLED` | `true` | Bootstrap `dlq_*` streams + route terminal failures |
| `FORGE_DLQ_RETENTION_DAYS` | `7` | Age-based DLQ index/stream cleanup |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Event schemas

Schemas live in [`contracts/events/`](../../contracts/events/) and are loaded at
startup. Readiness fails if the directory is missing or a schema file cannot be
compiled. Publish validates `data` against the subject’s latest schema (or
`headers.schema_version`). Failures return `422` with `{error, subject, violations}`
(payload values are never echoed).

`GET /v1/schemas` lists subjects + versions; `GET /v1/schemas/{subject}` returns
the versioned schema documents.

## DLQ

Messages that exhaust `max_deliveries` are published to `dlq.<family>.entry`
with failure metadata headers. Operators can list, inspect, redeliver, and delete
DLQ entries via `/v1/dlq`.

## Auth

No auth on publish/consume/DLQ yet. Identity tokens land in step `11.06`.

## Status

Step `11.05` — platform event JSON Schemas with publish-time validation.
Idempotency + consumer identity are `11.06`.
