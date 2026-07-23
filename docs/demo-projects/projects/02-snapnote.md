# Demo 2 — SnapNote

**Epic:** [`52-demo-snapnote`](../../implementation/epics/52-demo-snapnote.md) · **Focus:**
object storage, the event **queue** + background **workers**, and **worker autoscaling** by queue
depth (with optional node scale-up).

A small **notes app with file attachments**. You write a note and attach an image or PDF; the
upload goes to object storage, an event is published, and a **background worker** processes the
attachment (generates a thumbnail / extracts text) and writes the result back. The UI shows the
thumbnail appear **asynchronously** — the visible proof that the queue and workers did their job.

---

## 1. Why this product

This is the platform's async story: durable object storage + a durable queue + workers that scale
with backlog. It answers "can I upload a file and have work happen out-of-band, reliably, and does
the platform add workers when the queue backs up?"

## 2. Services exercised

| Service | How SnapNote uses it | Proven by |
|---|---|---|
| forge-storage | Presigned PUT for attachments; worker GETs the object; thumbnail PUT back. | File downloadable; thumbnail object exists. |
| forge-events | API publishes `attachment.uploaded` to a **durable queue**; worker consumes with ack + idempotency. | Event delivered exactly once per attachment. |
| forge-autoscaler | `ScalingPolicy { type: queueDepth }` on the worker → replicas rise with backlog, fall after drain; retry-in-flight blocks scale-down. | Worker replicas move with a burst upload. |
| forge-infrastructure (optional) | If worker replicas can't be placed, a Docker node is provisioned, then drained. | Node count rises under load, falls after. |
| managed Postgres | Note + attachment metadata (status: pending→ready). | Status flips after processing. |
| forge-gateway / build / control / runtime / observe | Baseline deploy + telemetry. | Host preflight; worker trace per job. |

## 3. Architecture

```text
Browser ──▶ Gateway :4000
  app.snapnote.localhost ─▶ snapnote-web
  api.snapnote.localhost ─▶ snapnote-api (Go)
        │ 1. create note + presign      ─▶ forge-storage (bucket: snapnote-attachments)
        │ 2. browser PUTs file to storage
        │ 3. publish attachment.uploaded ─▶ forge-events (queue: snapnote-attachments)
        └ metadata ─▶ Postgres (attachments.status=pending)

forge-events queue ─▶ snapnote-worker (Rust/Go)  [Worker resource, autoscaled]
        │ GET object ─▶ forge-storage → make thumbnail → PUT thumbnail
        └ UPDATE attachments.status=ready, thumbnail_key=...
```

## 4. Manifests (illustrative — `52.01`/`52.03`/`52.04`)

```yaml
kind: Application            # snapnote-api  (routes app./api.snapnote.localhost)
spec:
  dependencies:
    storage: { type: object, bucket: snapnote-attachments }
    queue:   { type: durable, name: snapnote-attachments }
    database:{ type: postgres, name: snapnote-db }
---
kind: Worker
metadata: { name: snapnote-worker, project: snapnote, environment: local }
spec:
  image: registry.forge.internal/snapnote/snapnote-worker:latest
  consumes: { queue: snapnote-attachments }
  scaling:
    minReplicas: 1
    maxReplicas: 8
---
kind: ScalingPolicy
metadata: { name: snapnote-worker-queue }
spec:
  target: { kind: Worker, name: snapnote-worker }
  policies:
    - { type: queueDepth, queue: snapnote-attachments, targetPerReplica: 20 }
```

## 5. Data model

```text
notes(id, title, body, created_at)
attachments(id, note_id → notes.id, object_key, content_type,
            status[pending|ready|failed], thumbnail_key, created_at, updated_at)
```

## 6. E2E scenario (`tests/e2e/projects/02-snapnote/spec.ts`)

1. Open `app.snapnote.localhost`, create a note "Trip photos".
2. **Attach an image** → browser uploads to storage; row shows **"processing…"**.
3. **Poll** until the note shows a **thumbnail** (status `ready`) — proves publish→consume→storage
   round-trip. Assert within a generous timeout.
4. **Burst:** upload N (e.g. 40) attachments quickly to build queue backlog.
5. Watch a small in-app "workers" indicator (reads platform autoscaler status) show **replicas
   increasing**; all attachments eventually reach `ready` (backlog drains).
6. After drain, replicas **scale back down** toward `minReplicas`.

### Platform assertions (→ findings)
* Every published `attachment.uploaded` is consumed **exactly once** (no duplicate thumbnails, no
  lost attachment) — restart the worker mid-burst to test redelivery/idempotency.
* Worker `ScalingPolicy.status` shows recommendation tracking `queueDepth`; replicas stayed within
  `[min,max]`; scale-down did not happen while messages were in-flight/retrying.
* Thumbnail object is retrievable via Storage and referenced correctly.
* (If node pressure triggered) Infrastructure provisioned then drained a Docker node.

## 7. Likely findings hotspots

Queue delivery semantics under consumer restart (at-least-once vs duplicates), visibility timeout,
presigned URL expiry/permissions, queueDepth metric freshness feeding the autoscaler, scale-down
safety while retries pending.

## 8. Acceptance criteria

* `make demo DEMO=52` + `02-snapnote` E2E pass headed and headless.
* Upload → async thumbnail appears; metadata flips `pending`→`ready`.
* Burst uploads visibly scale workers up then down within bounds; backlog fully drains.
* Exactly-once processing survives a worker restart mid-burst.
* Zero blocker findings attributed to SnapNote.

## 9. Steps → see epic

`52.01` scaffold+db · `52.02` storage + presigned upload · `52.03` events queue + worker +
idempotency · `52.04` worker autoscaling (+ optional node pressure) · `52.05` E2E browser spec ·
`52.06` demo + gate. Details: [epic 52](../../implementation/epics/52-demo-snapnote.md).
