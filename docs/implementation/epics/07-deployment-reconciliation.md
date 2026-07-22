# Epic 07: Deployment reconciliation

## Status

In progress (2/6)

## Goal

Give Forge a control loop that continuously drives **actual** deployment state toward **desired** deployment state. When this epic is done, Control owns a desired/actual replica model, a reconciliation controller performs single-replica convergence, rolling updates start new replicas and shift traffic before stopping old ones, unhealthy rollouts roll back automatically within a deployment timeout, every transition is recorded in deployment history, and the controller survives its own restart without duplicating or orphaning workloads. The capability is proven by `demos/07-rolling-deployment` deploying v1 → v2 with no total downtime and rolling a broken v3 back to v2.

## Why this epic exists

Steps 02–06 give a control plane that records desired state and a runtime/gateway/build path that can materialize a single workload on demand. Nothing yet keeps `actual` equal to `desired` over time: a crashed container stays dead, a new image version requires manual container juggling, and a bad release has no safety net. This epic introduces the reconciliation controller — the piece that makes Forge behave like a platform rather than a set of manual scripts — and the rollback safety that later epics (multi-node scheduling, workflows) build on.

## Primary code areas

* `services/forge-control/` — reconciliation controller module (Kotlin), desired/actual state stores, deployment history persistence
* `services/forge-runtime/` — workload create/stop/status APIs consumed by the controller (already built in epic 04; extended only if a gap is found)
* `services/forge-gateway/` — traffic-shift hooks for rolling updates (consumes Control/Runtime endpoint data from epic 05)
* `demos/07-rolling-deployment/` — Compose demo, fixture app versions v1/v2/v3, acceptance script
* `contracts/openapi/` — controller + deployment status API surface

## Suggested language

Kotlin, as a module inside Forge Control (per `specs.md` Step 07: "Kotlin within Forge Control initially. It may later be extracted."). The controller is designed with a clean seam so it can be extracted to a standalone service later without changing its persisted contracts.

## Spec references

* `specs.md` → Step 07: Reconciliation and deployment controller (desired/actual, rolling deploy, readiness verification, timeout, automatic rollback, restart failed instances, deployment history)
* `specs.md` → Step 04 (Runtime) for workload lifecycle APIs the controller drives
* `specs.md` → Step 05 (Gateway) for health-aware traffic shifting
* `docs/implementation/MASTER_PLAN.md` → Epic 07 catalog + dependency DAG

## Dependencies

* Epic [`04-forge-runtime`](04-forge-runtime.md) — workload create/start/stop/status + health probing (`04.03`–`04.06`), Control integration (`04.07`)
* Epic [`05-forge-gateway`](05-forge-gateway.md) — health-aware upstream selection so traffic can shift between replicas (`05.03`–`05.04`)
* Epic [`02-forge-control`](02-forge-control.md) — deployments (desired state) API + audit records (`02.05`)
* Epic [`06-forge-build`](06-forge-build.md) — recommended for producing the multi-version images used in the demo (not strictly required if images are pre-built)

## Out of scope for this epic

* Multi-node placement and scheduling across runtime agents (epic 08)
* Autoscaling / metrics-driven replica counts (not in `specs.md` at this stage)
* Blue-green or canary-percentage traffic splitting beyond the rolling one-at-a-time strategy
* Extracting the controller into a separate binary (kept as a Control module; seam only)
* Authn/authz on the controller APIs (arrives with epic 09; dev bypass documented)

## Success demo

```bash
make demo DEMO=07
```

`demos/07-rolling-deployment` runs two scenarios end to end:

```text
Scenario A — healthy rolling update
  deploy v1 (replicas=2) → converge → curl shows "v1"
  deploy v2 (replicas=2) → rolling update → curl never fails → curl shows "v2"

Scenario B — unhealthy rollout + rollback
  deploy v3 (readiness always fails)
  controller waits until deployment timeout
  controller rolls back to v2 automatically
  deployment history shows: v2 → v3 (failed) → rolled back to v2
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [07.01](../steps/07-deployment-reconciliation/07.01-desired-actual-model-and-controller-skeleton.md) | Desired/actual replica model + controller skeleton | Complete | Data model + no-op loop + status API |
| [07.02](../steps/07-deployment-reconciliation/07.02-single-replica-reconcile-loop.md) | Single-replica reconcile loop | Complete | Converge create/stop; restart crashed replica |
| [07.03](../steps/07-deployment-reconciliation/07.03-rolling-update.md) | Rolling update (start new → ready → shift → stop old) | Not started | No total downtime; Gateway traffic shift |
| [07.04](../steps/07-deployment-reconciliation/07.04-unhealthy-rollout-automatic-rollback.md) | Unhealthy rollout → automatic rollback | Not started | Deployment timeout + rollback to last-good |
| [07.05](../steps/07-deployment-reconciliation/07.05-deployment-history-and-restart-safety.md) | Deployment history + controller restart safety | Not started | Durable history; idempotent recovery |
| [07.06](../steps/07-deployment-reconciliation/07.06-demo-07-rolling-deployment.md) | Demo `07-rolling-deployment` + gate | Not started | Both scenarios; epic acceptance gate |

## Assumptions

* The controller runs inside Forge Control as a background loop (coroutine/scheduled executor) with a bounded reconcile interval (default **2s**), not an event bus. Events (epic 11) are not a dependency.
* "Replica" for epic 07 means N identical single-node containers of one service version; multi-node placement is deferred to epic 08 (the controller asks Runtime to run/stop; it does not yet choose a node).
* Traffic shift is expressed by marking a replica ready/not-ready and letting Gateway's health-aware selection (`05.04`) route only to ready replicas; the controller does not implement its own proxy.
* Deployment timeout default is **120s** and rollout batch size is **1 replica at a time** for the demo; both are configurable.
* Auth is bypassed via `FORGE_AUTH_MODE=dev` until epic 09; documented as temporary.
* Deployment history is persisted in the Control Postgres database created in epic 02.

## Open questions

* Should readiness verification poll Runtime health (`04.04`) or subscribe to a push status stream? Assumption: poll on the reconcile interval; revisit if it is too slow for the demo.
* Is "last known good version" tracked per service explicitly, or inferred from history? Assumption: store an explicit `last_healthy_deployment_id` on the service row for deterministic rollback.
* When the controller restarts mid-rollout, does it resume the rollout or restart it? Assumption: it re-derives intent from persisted desired/actual state and continues converging (idempotent), not resuming a step machine.
* Do we need per-replica readiness gates in Gateway, or is service-level readiness enough for the demo? Assumption: per-replica upstream health from `05.04` is sufficient.

## Next step to implement

**[07.03](../steps/07-deployment-reconciliation/07.03-rolling-update.md) — Rolling update** (start new → ready → shift → stop old).
