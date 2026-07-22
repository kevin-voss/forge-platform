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

**Extract path:** this module can move to a standalone service on port `4108`
without changing the place/persist contract — wire HTTP or RPC over
`Scheduler` + `PlacementStore` and leave Control as a client.
