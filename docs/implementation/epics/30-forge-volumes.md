# Epic 30: Forge Volumes

## Status

Planning

## Milestone

**M2 — Production platform.** Stateful workloads beyond managed Postgres — file uploads, caches, embedded databases, model weights — need provider-independent persistent volumes; this is a new M2 capability with no epic-18-style shipped precedent of its own.

## Goal

Stand up Forge Volumes — a Go service on port `4117` — providing provider-independent persistent volumes: create, attach, detach, mount, resize, snapshot, and restore, with access modes (`ReadWriteOnce`, `ReadOnlyMany`, `ReadWriteMany`) and storage classes `local`, `provider-block`, `replicated`, `high-performance`, and `archive`. A `Volume` resource declares `size`, `class`, `accessMode`, and `replicationFactor`. The initial implementation backs `local` volumes with Docker named volumes, `provider-block` with the target's native block disks (Hetzner Volumes, AWS EBS, Azure Managed Disks) attached with explicit node affinity, and lays the groundwork for a Forge-owned distributed block-storage engine for the `replicated` class. Proven by `demos/30-persistent-volumes`: attach, write, reschedule the workload to a different node, resize online, and restore from snapshot with data intact throughout.

## Why this epic exists

Every workload epic so far assumes ephemeral container filesystems. Real products need durable, attachable storage that survives a container restart, a node reschedule (epic 08), and — for the higher storage classes — a node failure. Building this as a first-class `Volume` resource rather than ad-hoc per-provider disk wiring keeps the platform's "one manifest, every target" promise intact for stateful workloads that are not databases or object buckets.

## Relationship to shipped epics

New capability, additive to **epic 04 — Forge Runtime** and **epic 02 — Forge Control**. Runtime's workload-create contract (`04.03`) gains a new, optional, additive field — `volumeMounts: []` — defaulting to empty, so every workload created before this epic keeps starting exactly as it does today with no mount. Control gains a new resource kind, `Volume`, following the same spec/status/finalizer envelope every other resource uses (epic 02's model); no existing Control endpoint changes shape.

## Primary code areas

* `services/forge-volumes/` — new Go service: `Volume` resource, storage-class drivers, snapshot/restore orchestration
* `services/forge-runtime/` — mount/attach execution at container-create time (extends `04.03`)
* `services/forge-infrastructure/` — provider block-disk adapters (future epic 23)
* `demos/30-persistent-volumes/`
* `contracts/openapi/forge-volumes.openapi.yaml`

## Suggested language

Go for the Forge Volumes controller/API, matching the orchestration-style new services (Build, Gateway, Events, Queue); node-level mount and attach execution is implemented as an additive capability inside `forge-runtime` (Rust), which already owns container lifecycle and filesystem operations.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Volumes
* `specs.md` → Step 04: Forge Runtime (workload create contract this epic extends)
* [`epics/04-forge-runtime.md`](04-forge-runtime.md) → `04.03` env/mount injection at create

## Dependencies

* [`04-forge-runtime`](04-forge-runtime.md) — mount/attach execution at workload create
* `23-forge-infrastructure` — provider block-disk adapters (future M1 epic)
* `08-multi-node-scheduler` — node affinity so a volume and its workload land together
* `20-declarative-resource-api` — `Volume` resource conventions

## Out of scope for this epic

* Building a full Forge-owned distributed block-storage engine to production maturity in this epic — the `replicated` class ships with a working engine sufficient for the gate demo; hardening it into the long-term "embedded or packaged open-source engine" is tracked as ongoing maturity work within this epic, not a blocking M2 requirement
* Cross-provider volume migration (moving a volume's bytes from a Hetzner disk to an AWS EBS disk)
* Cross-provider snapshot cloning
* A general-purpose CSI (Container Storage Interface) plugin surface — Forge Volumes is a Forge-native resource, not a Kubernetes CSI implementation

## Portability contract

A product manifest declares only `volume: {size: 10Gi, class: local | replicated}` — never an EBS volume type (`gp3`, `io2`), an Azure Managed Disk SKU, or a Hetzner Volume id. Storage classes map identically everywhere:

* `local` → a Docker named volume (Docker target) or host path (bare metal) on the node the workload is scheduled to.
* `provider-block` → a cloud block disk attached to the VM via the `InfrastructureProvider` adapter (Hetzner Volumes, AWS EBS, Azure Managed Disk) — an optional adapter, present only where the target provider offers block disks.
* `replicated` / `high-performance` / `archive` → the Forge-owned volume engine, identical on every target since it depends only on raw local disk, never on a specific provider primitive.

## Success demo

```bash
make demo DEMO=30
```

```text
Volume invoice-uploads: size 10Gi, class replicated, accessMode ReadWriteOnce, replicationFactor 2
  → attached to invoice-api, mounted at /data
  → app writes a fixture file → snapshot taken
  → workload rescheduled to a different node (epic 08) → volume detaches, reattaches, data intact
  → resize to 20Gi online, no workload restart
  → restore from snapshot into a new volume → fixture file present, byte-identical
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 30.01 | `Volume` resource + create/attach/detach/mount | Resource envelope; lifecycle API |
| 30.02 | `local` storage class (Docker named volumes) + explicit node affinity | Baseline durability on the node the workload runs on |
| 30.03 | `provider-block` storage class | Attach cloud block disks via provider adapters |
| 30.04 | Resize + access modes | Online growth; `ReadWriteOnce` / `ReadOnlyMany` / `ReadWriteMany` |
| 30.05 | Snapshot + restore | Point-in-time volume copy and recovery |
| 30.06 | Node migration + consistency guarantees | Safe detach/reattach across a reschedule |
| 30.07 | `replicated` storage class (Forge-owned engine) | Multi-node replication for node-failure tolerance |
| 30.08 | `high-performance` + `archive` storage classes | Tiering for latency-sensitive and cold-data workloads |
| 30.09 | Demo `30-persistent-volumes` + gate | Attach → reschedule → resize → snapshot → restore |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* `Volume` follows the same spec/status/finalizer envelope as every other resource kind (epic 20's normative model); `status.phase` reflects `Pending | Attached | Detached | Terminating`.
* Node affinity is explicit and persisted on the volume record — a `replicated`-class volume's placement across nodes is tracked the same way scheduler placement is tracked for replicas (epic 08).
* The Forge-owned distributed engine for `replicated` starts as a straightforward primary-plus-replica block device (not full erasure coding); erasure coding is Forge Storage's concern (epic 31), not Volumes'.
* Snapshot/restore assumes a workload-provided quiesce hook (e.g., a pre-snapshot HTTP callback) for consistency; a snapshot without a quiesce hook is documented as crash-consistent, not application-consistent.
* Volume deletion is finalizer-gated exactly like Database and Bucket deletion — no silent cascade delete when a workload referencing a volume is removed.

## Open questions

* Does resize require a workload restart on any storage class? **Assumption:** no — resize is always online for classes this epic ships; a class that cannot support online resize is documented as a gap, not silently degraded.
* Is the Forge-owned distributed engine built from scratch or built on a packaged open-source engine (e.g., Longhorn-style or a lightweight replicated block device)? **Assumption:** the initial `replicated` implementation packages an existing open-source replicated block-storage engine driven by a thin Forge controller, deferring a fully custom engine to future hardening.
* How does `high-performance` differ operationally from `local`? **Assumption:** `high-performance` prefers node-local NVMe/SSD-backed paths where the node reports that capability; it is otherwise identical to `local` in this epic, with true performance isolation left as future work.
* What happens to a volume when its last referencing workload is deleted? **Assumption:** the volume is retained by default (`reclaimPolicy: retain`); an explicit `reclaimPolicy: delete` opts into automatic cleanup, keeping the safe default non-destructive.

## Next step to implement

**30.01 — `Volume` resource + create/attach/detach/mount** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `30.01-volume-resource-and-lifecycle-api.md` and assign its `N`).
