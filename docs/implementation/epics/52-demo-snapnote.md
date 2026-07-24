# Epic 52: Demo 2 — SnapNote (object storage + queue + worker autoscaling)

## Status

In progress

## Goal

A notes-with-attachments product that proves the platform's async story: upload a file to
**object storage**, publish to a **durable queue**, process it in a **background worker**, and
**autoscale the worker** by queue depth (with optional node scale-up) — verified by a headed
browser E2E where a thumbnail appears asynchronously and worker replicas visibly rise then fall.

## Why this epic exists

Storage + events/queue + scaling workers is the backbone of every real product that does work
out-of-band. SnapNote is the smallest product that exercises the full publish→consume→store loop
and demonstrates queue-depth autoscaling on a real backlog. Full design:
[`../../demo-projects/projects/02-snapnote.md`](../../demo-projects/projects/02-snapnote.md).

## Primary code areas

* `demos/52-snapnote/` — API (Go) + worker (Go/Rust) + SPA, Dockerfiles, resource docs, scripts.
* `tests/e2e/projects/02-snapnote/spec.ts`.

## Suggested language

Go API + a worker (Go or Rust) + minimal SPA.

## Spec references

* `docs/demo-projects/projects/02-snapnote.md`
* Epics 13 (storage), 11 (events), 24 (autoscaler — worker/queueDepth), 23 (infrastructure).

## Dependencies

* Epic **50** (harness) complete.
* Storage, Events, Autoscaler, (optional) Infrastructure, managed Postgres available.

## Out of scope for this epic

* AI processing of attachments (thumbnail/text-extract is deterministic, non-AI).
* Identity-protected access (kept open here; auth is TaskFlow's job).

## Success demo

`make demo DEMO=52`: upload an image → thumbnail appears asynchronously; a burst of uploads drives
the queue up, worker replicas scale up within bounds and drain the backlog, then scale down;
processing is exactly-once across a worker restart.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 52.01 | Product scaffold + notes CRUD + Postgres | Complete | API + SPA, notes/attachments schema, baseline deploy + routes |
| 52.02 | Object storage integration | Complete | bucket, presigned PUT/GET, attachment upload + retrieval |
| 52.03 | Events queue + worker + idempotency | Complete | publish `attachment.uploaded`; durable consume + ack; exactly-once thumbnailing |
| 52.04 | Worker autoscaling (+ optional node pressure) | Not started | `ScalingPolicy queueDepth`; burst load; scale up/down; retry blocks scale-down; optional infra node |
| 52.05 | E2E browser spec | Not started | upload→async thumbnail→burst→watch replicas→drain; restart-mid-burst idempotency |
| 52.06 | Demo + epic gate | Not started | `demos/52-snapnote`; `make demo DEMO=52`; wired into test-platform-e2e |

Ordering + `N`: [`../steps/52-demo-snapnote/README.md`](../steps/52-demo-snapnote/README.md).

## Open questions

* Which durable-queue surface (forge-events durable subject vs a future forge-queue, epic 28) is
  the intended worker source today? Use forge-events; if semantics are insufficient, record a
  finding referencing epic 28.
