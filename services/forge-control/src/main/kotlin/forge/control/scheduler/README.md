# Scheduler module (extract seam)

This package is Forge Control's **placement boundary**. Callers (reconciler,
HTTP placement API) depend only on:

* [`Scheduler`](Scheduler.kt) — `place(PlacementRequest) → PlacementDecision`
* [`PlacementStore`](PlacementStore.kt) — persist replica → node assignments
* model types under `model/`

No Control HTTP, reconcile, or repository types leak into the scheduler
decision path. `SchedulerFactory` selects `first-fit`, `least-allocated`
(default), or legacy `single-node` from `FORGE_SCHEDULER_STRATEGY`.

**Fleet inventory (08.02):** `NodeStore` + `LivenessMonitor` persist Runtime
registration/heartbeats and expose `GET /v1/nodes` with capacity, allocation,
and `online`/`offline` status for placement strategies to consume.

**Strategies (08.03):** `FirstFitScheduler` and `LeastAllocatedScheduler` place
across online nodes with capacity checks. `CapacityReservation` atomically bumps
`allocation_json.slots` so concurrent placements cannot over-commit; release is
available via `PlacementService.releasePlacement` for stop/reschedule (08.05).

**Pending queue + anti-affinity (08.04):** soft/hard spread; unplaceable requests
persist as `pending` and drain via `QueueProcessor` when capacity frees.

**Reschedule on offline (08.05):** `NodeOfflineHandler` (grace-gated) marks an
offline node's placements `lost`, frees capacity, and requests replacements
(`rescheduled_from_node`). No-capacity cases reuse the pending queue.
`StaleReplicaFencer` stops surplus replicas when a recovered node would exceed
desired count. Flow is idempotent via `recoverLostReplicas()` on Control start.

**Resource requests/limits (25.01):** `RequirementsResolver` derives CPU/memory
from slots (or the reverse). Nodes store `allocatable_json` (capacity ×
overcommit − reserved). Placement filters emit structured
`unschedulable_reasons` and a `trace.filters[capacity]` record.
`GET /v1/placements/{id}` returns requests/limits/trace. Runtime reports host
capacity and optionally enforces Docker limits (`FORGE_ENFORCE_LIMITS`).

**Labels / taints / platform (25.02):** Nodes carry merged labels (`forge.dev/*`
reserved + pool + agent), taints, and architecture/OS. Placement applies
`node_selector` → `platform` → `taints` filters after capacity and records each
in `trace.filters`. `NoExecute` taint additions evict non-tolerating placements
via the 08.05 reschedule path. Runtime sets labels/taints via
`FORGE_NODE_LABELS` / `FORGE_NODE_TAINTS` and reports host arch/OS.

**GPU / reservations / stateful (25.05):** Node capacity may include GPU
(count/vendor/model/memory/driver). Placement requests with `requirements.gpu`
match only eligible nodes (`InsufficientGpu` otherwise). TTL `Reservation`
holds capacity until consumed or expired (`POST /v1/reservations`). Stateful
specs pin volume locality and protect primaries (`migrationPolicy:
manual-approval`) from preemption/drain unless a migration approval exists.

**Demo gate (08.06):** `make demo DEMO=08` (`demos/08-multi-node`) runs two
Runtime agents, asserts 2+2 placement distribution, then stops `node-b` and
checks reschedule onto `node-a` via `/v1/nodes` + `/v1/placements`.

**Extract path:** this module can move to a standalone service on port `4108`
without changing the place/persist contract — wire HTTP or RPC over
`Scheduler` + `PlacementStore` and leave Control as a client.
