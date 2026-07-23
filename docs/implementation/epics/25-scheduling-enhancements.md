# Epic 25: Scheduling enhancements

## Status

In progress

## Milestone

**M1 — Standalone cloud core.** Closes M1 out: after this epic, one manifest schedules correctly on a mixed-architecture, mixed-provider, mixed-zone fleet, survives node loss with bounded blast radius, and lets an operator express "this matters more than that" without hand-placing workloads.

## Goal

Turn the epic-08 scheduler from a slots-and-spread prototype into Forge's real placement engine: CPU/memory/disk requests and limits with enforcement, node labels/taints/tolerations and architecture/OS constraints, workload affinity and topology-aware spreading, priority classes with preemption and disruption budgets, and GPU/reservation/stateful placement rules. When this epic is done, every placement decision is deterministic and explainable — strategy, eliminated candidates, and score are all recorded — and a 3-replica HA workload spread across 2 zones loses exactly one replica when one node dies. Proven by `demos/25-ha-placement`, which is also the M1 exit gate.

## Why this epic exists

Epic 08 proves the shape of scheduling (a seam, a fleet, two strategies, soft spread, a queue, reschedule-on-loss) with a placeholder resource model (abstract slots) and no way to express "don't put this here," "keep this near/away from that," or "this workload outranks that one." Production placement needs real resource accounting so nodes are never actually over-committed, node/architecture constraints so a mixed Docker/bare-metal/Hetzner/AWS/Azure fleet stays correct, topology-aware spread so failure domains are respected (not just "a different node"), and priority/preemption so scarce capacity goes to what matters. Without this epic, epic 08's scheduler is a demo toy; with it, it is the platform's actual capacity-management surface — and the one later epics (autoscaler, deployment strategies, database HA, volumes) build on rather than route around.

## Relationship to shipped epics

This epic is **strictly additive** to epic 08. It extends, and never replaces:

* The seam: `Scheduler.place(PlacementRequest) → PlacementDecision` keeps its exact signature. New scheduling inputs (requests/limits, selectors, tolerations, affinity, topology spread, priority class, GPU, pinning) are additional optional fields on `PlacementRequest`; new outputs (trace, unschedulable reasons, preemption record) are additional optional fields on `PlacementDecision`. No field is removed or renamed.
* The `placements` table (`08.01`/`08.04`/`08.05`): every new column is nullable or has a default that reproduces epic-08 behavior exactly (e.g. `priority_class` defaults to a `default` class with `preemption_policy=Never`, so a placement that never sets it neither preempts nor is preempted).
* The `nodes` table (`08.02`): capacity gains real units (`cpu_millis`, `mem_mb`, `disk_mb`) alongside — not instead of — `slots`; labels/taints/topology columns default to values that collapse every node into the single implicit domain epic 08 assumed (`zone='default'`, `region='default'`, `labels={}`, `taints=[]`).
* `first-fit` / `least-allocated` (`08.03`): unchanged as strategies; they now run against a richer, filtered, scored candidate set instead of a raw capacity check.
* Soft anti-affinity + pending queue (`08.04`): generalized from "same-service, node-only" into workload affinity/anti-affinity with configurable topology keys and `minimumDistinctNodes`; a plain `anti_affinity: soft|hard` request with no topology key behaves exactly as it did in `08.04`.
* Reschedule-on-node-loss (`08.05`): unchanged mechanism (`NodeOfflineHandler` → fresh placement request → reconciler creates). This epic adds priority-aware victim selection and disruption-budget checks to the *voluntary* eviction paths only; the involuntary node-loss path from `08.05` is untouched (a dead node cannot be asked for permission).

Compatibility rule for every step in this epic: **new field, new column, or new endpoint — never a rename, a required field, or a breaking response shape.** A `POST /v1/placements` body that only ever set `{deployment_id, replica_index, requirements: {slots}}` continues to produce byte-for-byte the same placement outcome it did under epic 08.

This epic also touches:

* Epic `04-forge-runtime` — Runtime gains real capacity introspection, limit enforcement at container creation, and (optional) GPU device exposure. Additive to Runtime's existing workload lifecycle (`04.03`–`04.06`); no existing Runtime endpoint changes shape.
* Epic `07-deployment-reconciliation` — disruption budgets are honored by the rolling-update path (`07.03`) the same way epic 08's reschedule already asks the scheduler for placements; the reconciler gains a pre-flight budget check, not a new deployment model.

## Primary code areas

* `services/forge-control/src/main/kotlin/forge/control/scheduler/` — extends the epic-08 module in place (same package, same interface)
* `services/forge-runtime/src/` — capacity introspection (`node.rs`), enforcement at container creation (`docker.rs`, `workload.rs`), GPU device exposure
* `contracts/openapi/forge-control.openapi.yaml` — additive schema/endpoint extensions
* `demos/25-ha-placement/` — zone-aware HA + preemption acceptance demo

## Suggested language

Kotlin, continuing epic 08's choice of a module inside Forge Control (no extraction to the reserved port `4108` in this epic — the seam stays optional/deferred exactly as epic 08 left it). Runtime-side capacity/enforcement/GPU work is Rust, extending the existing `forge-runtime` crate.

## Spec references

* `specs.md` → Step 08 (Multi-node scheduler) — this epic implements the items that step explicitly deferred: "bin-packing beyond first-fit/least-allocated," "resource enforcement/cgroup limits inside Runtime," and priority, which epic 08 has no concept of at all
* `docs/architecture/standalone-cloud.md` → § Scheduling and placement (M1) — provider-neutral placement model, topology vocabulary, and the M1 promise ("nodes and workloads autoscale," "workloads survive node loss") this epic is jointly responsible for with epics 08 and 24

## Dependencies

* Epic [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — hard dependency; every step in this epic extends its seam, tables, and strategies directly
* Epic [`04-forge-runtime`](04-forge-runtime.md) — node capacity introspection and container-level resource enforcement
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — rolling updates consult disruption budgets before removing a replica
* Epic [`23-forge-infrastructure`](../epics/23-forge-infrastructure.md) — `NodePool` is the intended source of propagated node labels/topology (`25.02`); until/absent that epic, Runtime agent env vars set the same fields directly for standalone/dev use
* Epic [`24-forge-autoscaler`](../epics/24-forge-autoscaler.md) — consumes this epic's structured unschedulable reasons (`25.01`) to decide when to add nodes, and honors disruption budgets (`25.04`) when draining a node for scale-down

## Out of scope for this epic

* Extracting the scheduler into a standalone service on port `4108` (seam only, same as epic 08 — still optional, still deferred)
* Proactive rebalancing or bin-packing optimization of already-placed replicas (still placement-time only; doubly true for `pinned` stateful placements, see `25.05`)
* Cross-control-plane / cross-region scheduling (epic `39-multi-region`)
* GPU driver installation, model runtime integration, or AI-workload-aware scheduling heuristics (epic `38-ai-infrastructure-scheduling`) — this epic only adds GPU as a schedulable resource dimension
* Cost-aware placement (epic `41-usage-quotas-and-cost`)
* Admission-time policy hooks beyond scheduling filters, e.g. "workloads must have an owner label" (epic `33-forge-policy`)
* Kubernetes API/CRD compatibility of any kind — the vocabulary (labels, taints, tolerations, priority classes) is familiar by design, the implementation is Forge's own and has no Kubernetes dependency anywhere

## Portability contract

* A product manifest may declare `spec.resources` (requests, and optionally `limits`), `spec.platform` (architecture/OS), `spec.placement` (node selector, tolerations, affinity, topology spread), and `spec.priorityClassName`. It must never declare a machine type, provider name, region/zone identifier, or node id — those are node facts assigned by the platform operator (via `NodePool`/`InfrastructureProvider` in epic 23, or `FORGE_NODE_*` env on Runtime for local/bare-metal use) and surfaced to placement only as opaque label/topology values.
* Topology keys are exactly `node`, `zone`, `region`, `provider` on every target — Docker Compose, bare metal, Hetzner, AWS, and Azure all populate the same four columns on `nodes`, so a `topologySpreadConstraints` stanza written against local Docker behaves identically once pointed at a cloud target.
* Resource enforcement (`25.01`) uses only Docker Engine resource-constraint primitives (CPU shares/quota, memory limit) — available identically wherever Runtime talks to a Docker-API-compatible engine, which is every supported target.
* GPU requests, capacity reservations, and limit enforcement are opt-in and default off (`FORGE_GPU_ENFORCEMENT`, `FORGE_ENFORCE_LIMITS` default `false`/`true` respectively, both overridable). A manifest that sets none of this epic's new fields schedules identically to how it would have under epic 08 alone, on every target.
* Priority classes and disruption budgets are platform-level (cluster-scoped `PriorityClass`, deployment-scoped budget) — never a provider-specific concept, never required for a workload to run.

## Success demo

```bash
make demo DEMO=25
```

```text
Label 4 nodes into 2 zones: node-a, node-b -> zone-a ; node-c, node-d -> zone-b
Deploy a low-priority filler workload pinned to node-d (fills its only slot)
Deploy a 3-replica high-priority service with hard spread:
  topologyKey=node   minimumDistinctNodes=3
  topologyKey=zone    minimumDistinctNodes=2
→ scheduler places on node-a, node-b, node-c (node-d full) — 3 distinct nodes, both zones represented
Stop node-a (simulated node loss)
→ node-a offline; its replica marked lost; only node-d has any node-level room
→ node-d is occupied by the low-priority filler — scheduler preempts it (priority: high > low)
→ lost replica rescheduled onto node-d; filler workload evicted and recorded in the preemption audit
→ final state: exactly one replica ever lost, spread constraints still satisfied, no over-commit
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [25.01](../steps/25-scheduling-enhancements/25.01-resource-requests-limits-and-capacity.md) | 168 | CPU/memory/disk requests and limits + real capacity accounting | Complete | Slots become a derived view; overcommit config; limit enforcement; unschedulable reasons |
| [25.02](../steps/25-scheduling-enhancements/25.02-labels-selectors-taints-tolerations.md) | 169 | Node labels, selectors, taints, tolerations, architecture/OS constraints | Complete | Label merger; nodeSelector/platform/taints filters + trace; NoExecute eviction; Runtime FORGE_NODE_LABELS/TAINTS |
| [25.03](../steps/25-scheduling-enhancements/25.03-affinity-and-topology-spread.md) | 170 | Workload affinity/anti-affinity + topology spreading | Complete | Hard/soft affinity; node/zone/region/provider; `minimumDistinctNodes`; HA flow |
| [25.04](../steps/25-scheduling-enhancements/25.04-priority-preemption-and-disruption-budgets.md) | 171 | Priority classes, preemption, disruption budgets | Complete | Victim selection; graceful eviction; budgets honored by drains/rollouts; anti-starvation |
| [25.05](../steps/25-scheduling-enhancements/25.05-gpu-and-stateful-placement.md) | 172 | GPU, reservations, and stateful placement constraints | Not started | GPU requests/device exposure; reservations; volume affinity; pinning |
| [25.06](../steps/25-scheduling-enhancements/25.06-demo-25-ha-placement.md) | 173 | Demo `25-ha-placement` + epic gate | Not started | HA spread + reschedule + preemption; epic gate; M1 exit gate |

## Assumptions

* `NodePool` (epic 23) is the long-term source of propagated node labels, zone, region, and provider; until that epic ships, `FORGE_NODE_LABELS` / `FORGE_NODE_ZONE` / `FORGE_NODE_REGION` / `FORGE_NODE_PROVIDER` env vars on the Runtime agent set the same fields directly, and both paths are expected to coexist permanently (pool-level values merge with, and are overridden by, node-level values).
* "Slots" remain the default unit forever for callers that never adopt `requests`/`limits` — this is not a deprecation, it is a permanent compatibility view (conversion via `FORGE_SLOT_CPU_MILLIS` / `FORGE_SLOT_MEMORY_MB`).
* Only two taint effects are implemented — `NoSchedule` and `NoExecute` — matching exactly what the brief-level design calls for; a softer `PreferNoSchedule` is tracked as an open question, not built.
* GPU support and limit enforcement default to off so no CI runner or developer machine needs real GPUs or cgroup v2 support to pass `make demo DEMO=25`; they are proven functional in the demo only when the runner opts in.
* Capacity reservations and volume-affinity/pinning hints are set by platform-internal controllers (future epics 29/30) or a platform-operator API — never authored directly in a customer's `forge.yaml`.
* Preemption only ever targets replicas with a strictly lower `PriorityClass.value`; equal-priority workloads never preempt each other (avoids order-dependent flapping).

## Open questions

* Should there be a soft taint effect (`PreferNoSchedule`) alongside `NoSchedule`/`NoExecute`? Assumption: not in this epic — two effects are enough to prove dedicated-node and evacuation semantics; add a third only if a concrete future epic needs it.
* Should disruption budgets block *any* voluntary removal cluster-wide, or only removals initiated by drains/rollouts/preemption? Assumption: only platform-initiated voluntary paths (drain, rolling update, preemption) check the budget; a direct operator `forge delete` on a specific replica is not blocked by it (an explicit operator action is not a disruption to protect against).
* Where does `minimumDistinctNodes` draw its ceiling when the fleet is smaller than requested? Assumption: hard constraints that cannot be satisfied leave the excess replicas `pending` (reusing the `08.04` queue) rather than relaxing the constraint automatically.
* Does GPU model matching support ranges/classes (e.g. "any of A100/H100") or only exact string equality? Assumption: exact string equality on `model` in this epic; class-based matching is deferred to epic `38-ai-infrastructure-scheduling`.
* Is `pinned` settable by an application author at all, or strictly internal? Assumption: internal-only in this epic (set by future stateful controllers); exposing it as a manifest field is an open question for epics 29/30 to decide when they land.

## Next step to implement

**[25.05](../steps/25-scheduling-enhancements/25.05-gpu-and-stateful-placement.md) — GPU, reservations, and stateful placement constraints**
