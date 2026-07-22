# Epic 08: Multi-node scheduler

## Status

In progress

## Goal

Extend Forge from single-node execution to **placing workloads across multiple Runtime agents**. When this epic is done, multiple Runtime nodes register and heartbeat with reported resource capacity, a scheduler chooses a node for each replica using first-fit and least-allocated strategies plus anti-affinity, overloaded clusters queue pending workloads instead of over-committing, and workloads on a node that goes offline are rescheduled elsewhere. Proven by `demos/08-multi-node`: four replicas distributed across two simulated agents, then one agent stopped and its replicas rescheduled.

## Why this epic exists

The reconciliation controller (epic 07) converges replica counts but assumes one Runtime node. Real platforms spread load and survive node loss. This epic introduces scheduling as a distinct concern — capacity accounting, placement strategy, affinity, and failure-driven rescheduling — so the reconciler can ask "run N replicas" and the scheduler answers "on which nodes." It also establishes the node fleet model that later operational features rely on.

## Primary code areas

* `services/forge-control/scheduler/` — scheduler module (starts inside Control with a clean extract seam per the assumption below)
* `services/forge-runtime/` — multi-node registration, heartbeat, and resource reporting (extends epic 04 node APIs `04.02`)
* `services/forge-control/reconcile/` — reconciler consumes placement decisions (epic 07)
* `demos/08-multi-node/` — multiple simulated agents, distribution + reschedule acceptance
* `contracts/openapi/` — scheduler placement + node fleet APIs

## Suggested language

Kotlin, as a module under Forge Control, with an explicit extract seam so it can become a separate Go/Kotlin service on port `4108` later if size demands (per `specs.md` Step 08: "Go or Kotlin. Recommended: Go as a separate service" — deferred by the assumption below to reduce moving parts first).

## Spec references

* `specs.md` → Step 08: Multi-node scheduler (registration, heartbeat, resource reporting, first-fit, least-allocated, anti-affinity, reschedule on node loss, pending workloads)
* `specs.md` → Step 04 (Runtime node identity/registration `04.02`)
* `specs.md` → Step 07 (reconciler that drives desired replicas)
* `docs/implementation/MASTER_PLAN.md` → Epic 08 catalog + port `4108` reservation

## Dependencies

* Epic [`04-forge-runtime`](04-forge-runtime.md) — node identity + registration/heartbeat API (`04.02`), workload lifecycle
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — reconcile loop that requests replicas (`07.02`+)
* Epic [`02-forge-control`](02-forge-control.md) — Postgres + service model

## Out of scope for this epic

* Bin-packing beyond first-fit / least-allocated (e.g. spread, gang scheduling, priorities)
* Autoscaling the number of nodes or replicas
* Resource enforcement/cgroup limits inside Runtime (accounting only; enforcement is Runtime's concern)
* Extracting the scheduler into a standalone binary (seam only; optional later)
* Cross-region / topology-aware placement

## Success demo

```bash
make demo DEMO=08
```

```text
Start 2 simulated Runtime agents (node-a: 4 slots, node-b: 4 slots)
Deploy service with replicas = 4
→ scheduler places 2 on node-a, 2 on node-b (least-allocated), never > capacity
Stop node-b (heartbeat stops)
→ node-b marked offline; its 2 replicas rescheduled onto node-a (or pending if no capacity)
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [08.01](../steps/08-multi-node-scheduler/08.01-scheduler-skeleton-and-placement-apis.md) | Scheduler module/service skeleton + placement APIs | Complete | Module under Control; extract seam; placement API |
| [08.02](../steps/08-multi-node-scheduler/08.02-node-registration-heartbeat-resources.md) | Multi-node registration, heartbeat, resource reporting | Complete | Node fleet model + liveness |
| [08.03](../steps/08-multi-node-scheduler/08.03-first-fit-and-least-allocated-strategies.md) | First-fit and least-allocated strategies | Complete | Deterministic placement; capacity checks |
| [08.04](../steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) | Anti-affinity + pending queue | Not started | Spread replicas; queue when overloaded |
| [08.05](../steps/08-multi-node-scheduler/08.05-reschedule-on-node-offline.md) | Reschedule on node offline | Not started | Detect loss; recreate elsewhere |
| [08.06](../steps/08-multi-node-scheduler/08.06-demo-08-multi-node.md) | Demo `08-multi-node` + gate | Not started | Distribution + reschedule; epic gate |

## Assumptions

* The scheduler starts as a **module/package under Forge Control** in `08.01` with a clean interface (`Scheduler.place(request) → NodePlacement`) so it can be extracted to a standalone service on port `4108` later without changing its contract.
* Multiple Runtime agents in the demo are simulated by running several `forge-runtime` containers, each with a distinct node ID and a configurable slot capacity (`FORGE_NODE_SLOTS`); real per-node CPU/memory accounting is modeled as abstract "slots" plus optional CPU/memory fields.
* The reconciler (epic 07) delegates placement to the scheduler: it asks for a node per new replica instead of always targeting the local node.
* Node liveness is heartbeat-based: a node missing `FORGE_NODE_HEARTBEAT_TIMEOUT_S` (default 15s) of heartbeats is `offline`.
* Anti-affinity in this epic is "spread replicas of the same service across distinct nodes when possible" (soft by default, hard optional).
* Auth deferred to epic 09 (`FORGE_AUTH_MODE=dev`).

## Open questions

* Should placement be recomputed continuously (rebalance) or only for new/rescheduled replicas? Assumption: place-on-create + reschedule-on-loss only; no proactive rebalancing in this epic.
* Resource model granularity — abstract slots vs CPU/memory units? Assumption: slots as the primary unit, with optional CPU/memory fields carried for future strategies.
* Is anti-affinity hard (fail if impossible) or soft (best-effort then co-locate)? Assumption: soft by default with a per-service `anti_affinity: hard|soft` flag.
* Where does the reconciler learn a replica's node for status/adoption? Assumption: placement is persisted on the replica record and echoed in reconcile status.

## Next step to implement

**[08.04](../steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) — Anti-affinity + pending queue**
