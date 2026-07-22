# Epic 11: Forge Events

## Status

Planning

## Goal

Stand up Forge Events — a Go service on port `4105` — that provides durable publish/subscribe and background jobs over NATS JetStream, wrapped in a Forge abstraction. When this epic is done, services publish events and durably subscribe with acknowledgement and retry, failed messages land in a dead-letter queue that is inspectable, platform event types have JSON Schemas, idempotency keys and consumer identities prevent duplicate processing, and a polyglot demo (Go producer → Elixir consumer) proves cross-language delivery, retry, and DLQ. Proven by `demos/11-event-driven`.

## Why this epic exists

Platform features need to react to each other asynchronously — builds trigger deployments, node offline triggers reschedules, crashes trigger agent investigations. `specs.md` Step 11 defines a durable event backbone with retries, DLQ, schemas, and idempotency so that services stay decoupled and events survive consumer restarts. Later epics (Agents 15, Workflows 16) depend on this.

## Primary code areas

* `services/forge-events/` — new Go service (NATS JetStream wiring, publish/subscribe API, consumers, DLQ, schema registry)
* `contracts/events/` — JSON Schemas for platform event types
* `demos/11-event-driven/` — Go producer + Elixir consumer, retry + DLQ scenario
* `contracts/openapi/` — publish/subscribe/DLQ API surface

## Suggested language

Go (per `specs.md` Step 11), using NATS JetStream (provisioned in foundation `00`) for durable streams and consumers. Demo consumer is Elixir to prove polyglot consumption.

## Spec references

* `specs.md` → Step 11: Forge Events (publish, durable subscriptions, ack, retries, dead-letter, scheduled delivery, event schemas, consumer identities, idempotency keys; example event types)
* `specs.md` → Step 00 (NATS in foundation)
* `docs/implementation/MASTER_PLAN.md` → Epic 11 catalog + port `4105`

## Dependencies

* Foundation `00` — NATS JetStream running under Compose
* Epic `01-runtime-contract` — service health/log conventions
* Epic `09-forge-identity` — optional consumer/publisher identity via tokens (consumer identity itself is `11.06`)

## Out of scope for this epic

* Replacing NATS with a custom storage engine (`specs.md` says "explored later")
* Exactly-once delivery guarantees (at-least-once + idempotency keys instead)
* Full event-driven wiring of every platform service (producers/consumers added by their own epics; this provides the backbone + demo)
* Cross-cluster/federated NATS

## Success demo

```bash
make demo DEMO=11
```

```text
Go producer publishes application.crashed events
    ↓ Forge Events (NATS JetStream, durable)
Elixir consumer processes them (ack)
Inject a failing message → retried N times → lands in DLQ
Inspect DLQ via API
Restart consumer mid-stream → no lost/duplicate processing (idempotency)
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [11.01](../steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) | Skeleton + NATS wiring | Not started | Go service on 4105; JetStream connection |
| [11.02](../steps/11-forge-events/11.02-publish-subscribe-api.md) | Publish/subscribe API | Not started | Publish + subscribe surface |
| [11.03](../steps/11-forge-events/11.03-durable-consumers-ack-retry.md) | Durable consumers, ack, retry | Not started | Survive restart; retry policy |
| [11.04](../steps/11-forge-events/11.04-dlq-and-inspect-apis.md) | DLQ + inspect APIs | Not started | Dead-letter + inspection |
| [11.05](../steps/11-forge-events/11.05-event-json-schemas.md) | Event JSON Schemas | Not started | build.*, deployment.*, runtime.node.offline, application.crashed, agent.run.* |
| [11.06](../steps/11-forge-events/11.06-idempotency-keys-and-consumer-identity.md) | Idempotency keys + consumer identity | Not started | Dedup + named consumers |
| [11.07](../steps/11-forge-events/11.07-demo-11-event-driven.md) | Demo `11-event-driven` + gate | Not started | Go→Elixir; retry+DLQ; epic gate |

## Assumptions

* NATS JetStream is the transport; Forge Events exposes a stable HTTP(+optional gRPC) abstraction so producers/consumers do not couple directly to NATS client details. HTTP is the primary surface for the demo; a lightweight subscribe mechanism (long-poll/SSE or webhook push) is chosen in `11.02`.
* Streams are per event-type-family (`build`, `deployment`, `runtime`, `application`, `agent`) with subjects like `build.completed`; durable consumers are named per consumer identity.
* Delivery is at-least-once; idempotency keys (`11.06`) let consumers dedup. Ordering guaranteed only within a stream/subject as NATS provides.
* Retry policy is max-deliveries with backoff; exceeding it routes the message to a DLQ stream. Scheduled delivery uses JetStream delayed/timed redelivery or a delay subject.
* Event schemas (`11.05`) live in `contracts/events/` as JSON Schema; publish validates against the registered schema and rejects malformed events.
* Auth is optional in this epic; when Identity is enabled, publisher/consumer identity can be a service-account token. Demo may run without auth for simplicity (documented).

## Open questions

* Subscribe delivery model: server push (webhook/SSE) vs client pull (long-poll/consume). Assumption: provide a pull-based `consume` endpoint for durable consumers + optional push webhook; demo uses pull for determinism.
* HTTP-only vs HTTP+gRPC. Assumption: HTTP for the epic; gRPC optional later.
* Where do DLQ messages live — a dedicated DLQ stream per source stream, or one global DLQ? Assumption: one DLQ stream per source family with the original subject retained in metadata.
* Idempotency store: NATS-native dedup window vs a Forge-side dedup table. Assumption: use NATS msg-id dedup window plus a Forge-side seen-key store for consumer-side idempotency.

## Next step to implement

**[11.01](../steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) — Skeleton + NATS wiring** (Go service on 4105, JetStream connection + stream bootstrap; unblocks publish/subscribe).
