# Scheduler module (extract seam)

This package is Forge Control's **placement boundary**. Callers (reconciler,
HTTP placement API) depend only on:

* [`Scheduler`](Scheduler.kt) — `place(PlacementRequest) → PlacementDecision`
* [`PlacementStore`](PlacementStore.kt) — persist replica → node assignments
* model types under `model/`

No Control HTTP, reconcile, or repository types leak into the scheduler
decision path. `SingleNodeScheduler` is the inert default strategy for 08.01;
later steps add fleet-aware strategies behind the same `Scheduler` interface.

**Extract path:** this module can move to a standalone service on port `4108`
without changing the place/persist contract — wire HTTP or RPC over
`Scheduler` + `PlacementStore` and leave Control as a client.
