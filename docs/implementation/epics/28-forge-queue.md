# Epic 28: Forge Queue

## Status

Planning

## Milestone

**M2 — Production platform.** Durable job queues are a named M2 promise: products need retry, dead-letter, priority, and worker-autoscaling semantics on top of the event backbone, not raw pub/sub.

## Goal

Stand up Forge Queue — a Go service on port `4115` — that exposes job-queue semantics on top of the epic 11 NATS JetStream backbone: named queues, delayed and scheduled jobs, retries with exponential backoff, priorities, dead-letter queues, deduplication, visibility timeout, lease renewal, worker heartbeats, batch receive, queue-depth and oldest-job-age metrics, and project isolation. A `Queue` resource declares `durability: replicated`, `delivery.maxAttempts`, `delivery.visibilityTimeout`, and `deadLetterQueue.enabled`. When this epic is done, worker pools scale on queue-depth signals fed to the autoscaler (epic 24), and a poison job lands in an inspectable dead-letter queue instead of looping forever. Proven by `demos/28-queue-autoscaling`.

## Why this epic exists

Epic 11 gives the platform a durable pub/sub and background-job transport, but "publish and durably consume with retry" is not the same contract application developers expect from a job queue: named queues, per-job priority, visibility timeout with lease renewal (so a slow worker doesn't get its job redelivered out from under it), and queue-depth metrics that an autoscaler can act on. Forge Queue is the job-semantics façade that makes Events usable as a product-facing queue without every product reimplementing retry/DLQ/lease logic on raw JetStream primitives.

## Relationship to shipped epics

Built **on top of epic 11 — Forge Events**. Forge Queue does not replace or modify Events' publish/subscribe API (`11.02`) or its durable-consumer, ack, and DLQ machinery (`11.03`–`11.04`); it wraps a specific JetStream stream + consumer pairing per `Queue` resource and adds job-shaped semantics (priority, visibility timeout, batch receive) that Events intentionally leaves generic. Every existing epic-11 producer/consumer keeps working unchanged; Forge Queue is a new, additive resource kind and a new API surface, not a modification of `forge-events`.

## Primary code areas

* `services/forge-queue/` — new Go service: `Queue` resource, job semantics layer over JetStream
* `services/forge-events/` — consumed unchanged as the underlying transport (no modification expected)
* `services/forge-autoscaler/` — worker-autoscaling integration point (future epic 24)
* `demos/28-queue-autoscaling/`
* `contracts/openapi/forge-queue.openapi.yaml` — queue/job/DLQ API surface

## Suggested language

Go — matches Forge Events, and keeps the two services' JetStream client code and operational tooling consistent.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Queue
* `specs.md` → Step 11: Forge Events (publish/subscribe, durable consumers, retries, dead-letter, idempotency keys)
* [`epics/11-forge-events.md`](11-forge-events.md) → `11.02`–`11.06`

## Dependencies

* [`11-forge-events`](11-forge-events.md) — JetStream transport this epic wraps
* `24-forge-autoscaler` — worker-autoscaling integration (future M1 epic)
* `09-forge-identity` — project-scoped authentication on queue APIs
* `20-declarative-resource-api` — `Queue` resource conventions

## Out of scope for this epic

* Replacing NATS JetStream as the transport (inherited unchanged from epic 11)
* Cross-project queue sharing (project isolation is a hard requirement, not a configuration)
* Exactly-once delivery guarantees (at-least-once + deduplication, same guarantee model as epic 11)
* A queue-browsing UI (console, epic 40)

## Portability contract

A product manifest declares only `queue: {type: durable, name: invoice-jobs}` — no NATS stream/consumer configuration, no provider queue ARN or connection string (no AWS SQS queue URL, no Azure Service Bus namespace). `Queue` behaves identically on local Docker, bare metal, Hetzner, AWS, and Azure because it is always backed by the platform's own JetStream cluster; a managed queue (SQS, Service Bus) may exist only as an optional adapter behind the same `Queue` API, never as a requirement to pass the gate demo.

## Success demo

```bash
make demo DEMO=28
```

```text
Queue invoice-jobs: durability replicated, delivery.maxAttempts 5,
                     delivery.visibilityTimeout 30s, deadLetterQueue.enabled true
  → producer enqueues 500 jobs, 3 scheduled 60s in the future
  → worker pool scales 1 → 8 replicas as queue depth crosses the autoscaler threshold
  → one poison job exceeds maxAttempts → moves to the dead-letter queue, inspectable via API
  → queue drains → oldest-job-age metric returns to 0 → workers scale back to 1
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 28.01 | `Queue` resource over named JetStream stream/consumer pairs | Declarative queue creation on top of epic 11 transport |
| 28.02 | Delayed + scheduled jobs | Deliver a job no earlier than a specified time |
| 28.03 | Retries with exponential backoff + dead-letter queues | Bounded retry; DLQ on exhaustion, inspectable via API |
| 28.04 | Priorities + deduplication | Priority-ordered receive; idempotency-key dedup on enqueue |
| 28.05 | Visibility timeout + lease renewal + worker heartbeats | Prevent duplicate processing of a slow-running job |
| 28.06 | Batch receive + queue-depth/oldest-job metrics | Bulk dequeue; metrics feed the autoscaler |
| 28.07 | Worker-autoscaling integration + project isolation | Wire queue-depth metric to epic 24; enforce project scope |
| 28.08 | Demo `28-queue-autoscaling` + gate | Enqueue → scale → poison job → DLQ → drain → scale down |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* Each `Queue` resource maps to exactly one JetStream stream and one durable consumer group; Forge Queue does not introduce a second storage engine.
* `durability: replicated` means the underlying JetStream stream uses NATS's own replica factor (already available in a multi-node NATS deployment); Forge Queue does not implement its own replication.
* Visibility timeout and lease renewal are implemented as a Forge Queue-side redelivery delay layered on top of JetStream's ack-wait, not a NATS protocol change.
* Batch receive size and worker heartbeat interval are per-queue configurable with platform-wide defaults.
* Dead-letter queues are themselves inspectable `Queue`-shaped resources (read-only receive, no re-enqueue without an explicit requeue call).

## Open questions

* Does deduplication use a client-supplied idempotency key or content-hash dedup? **Assumption:** client-supplied idempotency key, matching the convention epic 11's `11.06` already establishes; content-hash dedup is not implemented.
* Is priority a separate JetStream stream per priority level, or a single stream with priority-ordered consumer delivery? **Assumption:** priority levels map to separate underlying streams internally (simpler ordering guarantees), presented to callers as one logical `Queue` with a `priority` field on enqueue.
* How does the worker-autoscaler learn queue depth — polling Forge Queue's metrics endpoint, or a push event? **Assumption:** polling, consistent with epic 07's resolved approach to reconcile-loop signals; push-based scaling triggers are not required for this epic's gate.
* Should the dead-letter queue auto-expire messages, or retain them until manually purged? **Assumption:** DLQ messages are retained until manually purged or requeued; no automatic expiry in this epic.

## Next step to implement

**28.01 — `Queue` resource over named JetStream stream/consumer pairs** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `28.01-queue-resource-and-jetstream-pairing.md` and assign its `N`).
