# Epic 31: Distributed object storage

## Status

Planning

## Milestone

**M2 — Production platform.** Single-node object storage (epic 13) cannot survive a node failure; distributed, self-healing storage is a named M2 production requirement.

## Goal

Extend Forge Storage from a single-node local-filesystem backend into a distributed object store: object replication, erasure coding, node-failure tolerance, checksums, background repair jobs, bucket versioning, lifecycle policies, multipart uploads, signed URLs, storage quotas, region replication, object lock (WORM), and encryption at rest with per-organization keys. When this epic is done, an upload streams parts directly to multiple data nodes, a node failure drops a bucket below its target replication factor, and a repair controller restores it automatically by copying from a healthy replica — all while the bucket/object HTTP API stays exactly what epic 13 already shipped. Proven by `demos/31-storage-repair`.

## Why this epic exists

Epic 13 proved the bucket/object contract, integrity, quotas, and signed access against a single-node local-filesystem backend — sufficient to unblock every consumer (Build artifacts, database backups, model files, agent/workflow outputs) but a single point of failure. Production storage needs to survive a node loss without losing objects or breaking the API its consumers already integrated against.

## Relationship to shipped epics

Extends **epic 13 — Forge Storage**. The existing bucket/object HTTP API (`13.02`–`13.06`) is unchanged in shape; every consumer that integrated against it keeps working. The single-node local-filesystem backend becomes the `local` storage-class adapter, while a new `distributed` adapter adds replication and erasure coding; selection is a new, additive per-bucket field (`spec.storageClass`, default `local`) that preserves today's exact single-node behavior when unset. `13.05`'s signed-token mechanism and `13.06`'s quota model are extended with additive fields, not replaced.

## Primary code areas

* `services/forge-storage/` — extended (still port `4107`): replication, erasure coding, repair controller, versioning, lifecycle
* `demos/31-storage-repair/`
* `contracts/openapi/forge-storage.openapi.yaml` — additive fields for storage class, versioning, lock

## Suggested language

Rust — continues epic 13 unchanged; the streaming I/O and content-storage foundations from `13.01`–`13.04` are reused directly.

## Spec references

* `docs/architecture/standalone-cloud.md` § Distributed object storage
* `specs.md` → Step 13: Forge Storage
* [`epics/13-forge-storage.md`](13-forge-storage.md) → `13.02`–`13.06`

## Dependencies

* [`13-forge-storage`](13-forge-storage.md) — single-node baseline this epic extends
* `08-multi-node-scheduler` — data-node placement and liveness awareness
* `32-secrets-high-availability` — per-organization encryption keys for at-rest encryption
* `20-declarative-resource-api` — additive `Bucket.spec.storageClass` conventions

## Out of scope for this epic

* An S3-compatible API surface — the Forge-native bucket/object API from epic 13 remains the only contract
* Cross-provider storage federation (spanning a bucket across two different cloud providers simultaneously)
* Tiering to an external cold-storage service as anything but an optional adapter behind the `archive` lifecycle target

## Portability contract

A product manifest declares only `storage: {type: object, bucket: invoices}` — never a node count, replication factor, or provider bucket ARN (no S3 bucket ARN, no Azure Storage account name, no Hetzner Object Storage endpoint). Replication factor and erasure-coding parameters are cluster-operator configuration on the bucket's `StorageClass`, never in the product manifest.

* **Local Docker (single node, the CI target)**: replication factor collapses to 1 and erasure coding is disabled — a documented, expected degraded mode since there is only one data node. The bucket/object API and every consumer-visible behavior stay identical; only the redundancy guarantee is reduced.
* **Bare metal / Hetzner / AWS / Azure with 3+ storage nodes**: full replication and erasure coding are active, using only node-local disks the platform itself manages — no provider-specific object-storage service is required.

**Data-safety rules (non-negotiable):**

* Buckets and objects are **never cascade-deleted** — deletion is finalizer-gated, consistent with epic 13's existing explicit hard-delete requirement.
* The repair controller **only reads from a confirmed-healthy replica** as its copy source — an unconfirmed or suspect replica is never used to "restore" another replica.
* Checksums are verified **before** a write is acknowledged to the client, not after, so a corrupted upload is rejected rather than silently stored.
* Object lock (WORM) is enforced at the metadata layer independent of any single data node, so a locked object cannot be deleted by racing a node's local state.

## Success demo

```bash
make demo DEMO=31
```

```text
bucket invoices: replicationFactor 3, erasureCoding disabled
  → multipart upload streams parts to 3 data nodes → checksums verified before ack → replicas confirmed → metadata committed
  → one data node is killed
  → repair controller detects replicas at 2/3 → copies from a healthy source → factor restored to 3
  → signed URL requested, then used after expiry → rejected
  → lifecycle policy would expire an object after 30 days, but object lock blocks deletion before its retention date
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 31.01 | Object replication across data nodes + checksums | Multi-node writes; verify-before-ack integrity |
| 31.02 | Node-failure tolerance + repair jobs | Detect under-replication; repair from a healthy source |
| 31.03 | Erasure coding | Reduced-redundancy-cost durability for the `archive`/large-object path |
| 31.04 | Bucket versioning + lifecycle policies | Object history; scheduled expiry/transition rules |
| 31.05 | Multipart uploads + signed URLs | Extends `13.05`; streamed large-object upload |
| 31.06 | Storage quotas + region replication | Extends `13.06`; per-project quota, async cross-region copy |
| 31.07 | Object lock + encryption at rest with per-organization keys | WORM retention; per-org key isolation |
| 31.08 | Demo `31-storage-repair` + gate | Upload → node loss → repair → signed-URL expiry → object lock |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* `storageClass` is a new, additive, optional field on the existing `Bucket` resource; omitting it preserves epic 13's exact single-node local-filesystem behavior.
* Replication targets distinct nodes tracked via the epic 08 node fleet model; the repair controller consumes node liveness the same way the scheduler does for reschedule decisions.
* Erasure coding is applied selectively (large/archival objects) rather than universally, since small-object erasure coding has poor overhead characteristics; replication remains the default durability mechanism for small objects.
* Per-organization encryption keys are issued and rotated by Forge Secrets (epic 32), not managed independently inside Forge Storage.
* Object lock retention periods are enforced server-side and cannot be shortened once set, even by an authorized caller, matching typical WORM compliance semantics.

## Open questions

* How many data nodes are required before replication/erasure coding activate versus falling back to the single-node degraded mode? **Assumption:** replication factor 3 requires at least 3 distinct storage-capable nodes; below that threshold the bucket runs at whatever factor is achievable (down to 1) and reports a `Degraded` condition explaining why.
* Does the repair controller run continuously or on a triggered basis (node-offline event)? **Assumption:** both — a continuous background reconcile pass (matching every other Forge controller's restart-safe recompute model) plus an immediate check triggered by a node-offline signal from epic 08.
* Is versioning on by default for every bucket, or opt-in? **Assumption:** opt-in per bucket (`spec.versioning.enabled`), since versioning has real storage-cost implications and epic 13 shipped without it.
* Where do per-organization encryption keys come from before epic 32 ships its full envelope-encryption model? **Assumption:** this epic depends on epic 32's key-issuance API landing first; encryption-at-rest is not enabled until that dependency is satisfied, and buckets default to unencrypted-at-rest (relying on disk-level encryption) until then.

## Next step to implement

**31.01 — Object replication across data nodes + checksums** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `31.01-object-replication-and-checksums.md` and assign its `N`).
