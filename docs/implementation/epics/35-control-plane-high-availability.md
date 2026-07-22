# Epic 35: Control-plane high availability

## Status

Planning

## Milestone

**M2 — Production platform.** First of the three M2 hardening epics (35, 36, 37); 37 is the M2 exit gate.

## Goal

Make Forge Control and its controllers (epic 07) survive process loss without operator intervention and without duplicating work. When this epic is done, Control runs as multiple replicas against one shared Postgres, one replica holds a leased leadership per controller group, every external side effect a controller performs carries an operation id so retries are safe, and killing the leader process causes another replica to take over and resume reconciliation from persisted state within one lease TTL — with zero duplicated VM creates, zero duplicate rollouts, and zero lost desired-state writes. Proven by `demos/35-control-failover`.

## Why this epic exists

Every capability shipped so far — deployments (07), scheduling (08), and everything else through M1 — assumes exactly one Control process. A production platform cannot have a single point of failure in its control plane: a crashed or restarted Control process must not stall reconciliation, and two processes racing to reconcile the same resource must not double-provision. This epic adds the leader election, leasing, and idempotency discipline that let Control scale to N replicas safely, and it establishes the operation-id convention that every later controller (28 Queue, 29 Database HA, 30 Volumes, 33 Policy…) is required to use.

## Relationship to shipped epics

Extends **epic 02 Forge Control** (single Kotlin + Ktor process today) and **epic 07 deployment reconciliation** (controllers that assume they are the only writer). Compatibility rule: additive, not a rewrite —

* Control's public HTTP API (`/v1/projects/...`) is unchanged; every existing client (CLI, Gateway, demos 02–19) keeps working against any replica for reads.
* A new cluster-scoped `ControlLease` resource and an internal leader-only write gate are added *inside* Control; single-replica deployments (the default for every M0/M1 demo) behave exactly as epic 02 shipped them — one implicit leader, no lease contention.
* Epic 07's reconcile loop gains a "run only while holding my controller-group lease" guard; its reconcile logic itself does not change.

## Primary code areas

* `services/forge-control/ha/` — leader election, lease acquisition/renewal, controller-group sharding
* `services/forge-control/reconcile/` — lease-gated execution wrapper around epic 07's existing loop
* `services/forge-control/db/` — transactional resource updates, migration versioning
* `contracts/openapi/forge-control.openapi.yaml` — additive `ControlLease` read endpoints (no breaking changes)
* `demos/35-control-failover/` — 3-replica failover acceptance

## Suggested language

Kotlin, extending the existing Control service and its Postgres/Hikari/Flyway stack (per epic 02).

## Spec references

* `docs/architecture/standalone-cloud.md` § Control-plane high availability
* `specs.md` → Step 02 (Forge Control) and Step 07 (reconciliation) — the single-process baseline being made highly available

## Dependencies

* Epic [`02-forge-control`](02-forge-control.md) — domain model, Postgres ownership, error/idempotency conventions to extend
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — the controller loop that becomes lease-gated
* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — `generation`/`resourceVersion` optimistic-concurrency envelope that transactional updates rely on
* Epic `11-forge-events` (NATS JetStream) — substrate for the durable internal work queue and watch-recovery stream

## Out of scope for this epic

* Backup/restore of the Control database itself (epic 36)
* Cross-region control planes (epic 39)
* Policy/admission enforcement (epic 33)
* Plugin-loaded controllers (epic 43)
* Rewriting epic 07's placement or rollout logic — only its execution safety changes

## Portability contract

The product manifest never names Control's replica count, lease backend, or leader-election mechanism — those are install-time operator concerns configured on the Control deployment itself, never on an `Application`/`Database`/etc. resource. Leases are stored as ordinary rows in Control's own Postgres (no required external coordination service — no etcd, no Consul, no cloud load-balancer health-check dependency), so behavior is identical on local Docker, bare metal, Hetzner, AWS, and Azure: N Control containers/VMs, one shared Postgres, one lease table. Local Docker (the CI default) runs 1 replica for every other epic's gate and 3 replicas only for this epic's own gate.

## Success demo

```bash
make demo DEMO=35
```

```text
Start 3 forge-control replicas against one Postgres; replica A acquires the
controller-group lease and becomes leader
Deploy a demo service (replicas=2) — leader reconciles and places it
Kill replica A (SIGKILL)
→ lease expires after FORGE_CONTROL_LEASE_TTL_S
→ replica B acquires the lease, resumes reconciliation from persisted state
→ demo service still has exactly 2 running replicas — no duplicates, no gaps
Repeat op_<id> POST that replica A had in flight → replica B returns the same
terminal result instead of creating a second resource
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 35.01 | Multi-replica Control + shared Postgres wiring | Run N Control processes safely against one database |
| 35.02 | `ControlLease` resource + leader election | Cluster-scoped lease, TTL-based acquisition/renewal |
| 35.03 | Leader-gated write path + operation ids | Every external side effect keyed by `op_…`; retries return the same result |
| 35.04 | Controller sharding by lease group | Independent lease per controller family so one slow controller can't block others |
| 35.05 | Durable work queue + watch recovery on failover | New leader resumes in-flight work and SSE watch streams without gaps |
| 35.06 | Rolling control-plane upgrade support | Mixed-version replicas tolerate a rolling Control deploy |
| 35.07 | Demo `35-control-failover` + epic gate | Kill-leader acceptance; no duplicate work, no lost writes |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* Leases live in Control's own Postgres (a `control.leases` table with TTL + fencing token), not an external coordination service — keeping the "no required managed service" rule intact.
* Controller groups are coarse (e.g. one lease for "reconciliation", one for "scheduling hooks") rather than one lease per resource; per-resource locking is handled by `resourceVersion` optimistic concurrency, not leases.
* Idempotency keys (`op_…`) are generated by the initiating controller and persisted alongside the external action they authorize, so a crash-and-retry after partial completion is safe.
* The demo's 3-replica topology is Compose-only; production replica counts remain an operator choice.

## Open questions

* Fencing on split-brain (a "dead" leader that isn't actually dead, e.g. paused not killed): Assumption: every leader-only write carries the current lease's fencing token; Postgres rejects a write from a stale token even if two processes briefly believe they are leader.
* Granularity of controller sharding — one lease for all of Control vs. one per controller family: Assumption: per-family leases (reconciliation, scheduling, future HA-aware controllers) so one stuck controller doesn't stall unrelated ones.
* Read availability during leader transition — do reads block? Assumption: reads are never leader-gated (any replica serves consistent reads from Postgres); only writes and controller actions are leader-gated.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **35.01 — Multi-replica Control + shared Postgres wiring** first: it is the prerequisite substrate every later step in this epic (and epic 42's rolling control-plane upgrade) depends on.
