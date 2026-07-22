# Epic 36: Backup and disaster recovery

## Status

Planning

## Milestone

**M2 — Production platform.** Second of the three M2 hardening epics (35, 36, 37); 37 is the M2 exit gate.

## Goal

Replace every feature's ad hoc backup script with one platform-wide backup and disaster-recovery capability. When this epic is done, a `BackupPolicy` resource can target the Control database, Identity database, Secrets metadata, any `Database`, queues, object-storage metadata, workflow state, memory collections, registry metadata, and application manifests; backups run on schedule, are encrypted, have their integrity checked, and are periodically restore-tested automatically; and a full simulated cluster loss can be recovered — new control plane up, data restored, workloads recreated, Gateway routes restored — inside a documented RTO. Proven by `demos/36-disaster-recovery`.

## Why this epic exists

Epic 18 shipped `pg_dump`-based backup/restore for a single managed `Database`. A production platform needs the same guarantee — bounded RPO, tested restores, encryption at rest — for *every* stateful thing the platform owns, not just customer databases, and it needs a single operational story ("run `forge restore`") instead of N per-service scripts. This epic generalizes epic 18's pattern into a dedicated service and adds the one capability no per-feature script can provide on its own: recovering the platform *itself* after total loss.

## Relationship to shipped epics

Extends **epic 18 managed PostgreSQL** (per-`Database` `pg_dump`/restore, already shipped) by generalizing it under a central `BackupPolicy`; also backs up **epic 02** (Control DB), **epic 09** (Identity DB), **epic 10** (Secrets metadata — never plaintext secret values), **epic 13** (Storage metadata), **epic 16** (Workflows durable run state), **epic 17** (Memory collections), and future **epic 26** (Registry metadata). Compatibility rule: `forge-backup` is a new additive service that calls each target's existing, documented export/snapshot hook (a facade) rather than reading any service's database directly — epic 18's existing per-`Database` backup/restore commands keep working standalone, now orchestrated centrally when a `BackupPolicy` references them.

## Primary code areas

* `services/forge-backup/` — new Go service, policy engine, schedule runner, target adapters
* `services/forge-backup/adapters/` — one adapter per backup target (control-db, identity-db, secrets-metadata, database, queue, storage-metadata, workflow-state, memory, registry-metadata)
* `demos/36-disaster-recovery/` — full cluster-loss recovery acceptance

## Suggested language

Go, matching the other `forge-<capability>` infrastructure services (registry, deploy, queue).

## Spec references

* `docs/architecture/standalone-cloud.md` § Backup and disaster recovery
* `specs.md` → Step 18 (managed PostgreSQL backup/restore) — the per-database baseline being generalized

## Dependencies

* Epic [`18-managed-postgresql`](18-managed-postgresql.md) — per-`Database` backup/restore baseline
* Epic [`02-forge-control`](02-forge-control.md), [`09-forge-identity`](09-forge-identity.md), [`10-forge-secrets`](10-forge-secrets.md), [`13-forge-storage`](13-forge-storage.md), [`16-forge-workflows`](16-forge-workflows.md), [`17-forge-memory`](17-forge-memory.md) — backup targets
* Epic `30-forge-volumes` (catalogued, not yet materialized) — volume snapshot primitive reused for application-consistent snapshots
* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — resource envelope `BackupPolicy` is defined against

## Out of scope for this epic

* Control-plane process failover (epic 35 — this epic assumes total loss, not a leader crash)
* Streaming replication / live standby (epic 29 database high availability)
* Incident detection and automated rollback of a bad *deployment* (epic 37 — this epic restores data, not application health)
* A UI for backup management (surfaces read-only in epic 40's Console)

## Portability contract

A `BackupPolicy` never names a cloud storage provider, bucket ARN, or storage account — it references a Forge-owned backup target (a storage class resolved by the operator's install, defaulting to Forge Storage from epic 13/31). Local Docker (the CI default) writes backups to a local volume-backed object store; bare metal, Hetzner, AWS, and Azure use the identical `BackupPolicy` resource, differing only in which optional storage adapter is installed underneath — cloud object storage is always an optional adapter, never a requirement to pass the gate.

```yaml
apiVersion: forge.dev/v1
kind: BackupPolicy
metadata:
  name: platform-core
  organization: forge-labs
spec:
  targets: [control-db, identity-db, secrets-metadata, registry-metadata]
  schedule: "0 */6 * * *"
  retention: { daily: 7, weekly: 4, monthly: 3 }
  encryption: required
  verifyRestoreEvery: 7d
status:
  phase: Ready
  lastBackupAt: 2026-07-22T06:00:00Z
  lastVerifiedRestoreAt: 2026-07-15T06:00:00Z
```

## Success demo

```bash
make demo DEMO=36
```

```text
Run the platform with a BackupPolicy covering Control DB + one Database +
one Storage bucket; take a scheduled backup
Simulate total cluster loss: stop every container, discard all volumes
except the backup archive
Bring up a fresh, empty control plane
forge restore --from <backup-id>
→ infrastructure inventory reconciled → databases restored → storage
  metadata restored → application manifests reapplied → Gateway routes
  restored
Assert: project/environment/application hierarchy, database rows, and
bucket objects all match pre-loss state; RTO within the demo's documented
bound
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 36.01 | Backup service skeleton + `BackupPolicy` resource | Service scaffold, policy CRUD, schedule runner |
| 36.02 | Control-plane target adapters | Control DB, Identity DB, Secrets metadata, Registry metadata |
| 36.03 | Database, volume, and storage-metadata adapters | Reuse epic 18 backup + epic 30 volume snapshots + Storage metadata export |
| 36.04 | Encryption, integrity checks, automated restore testing | Encrypted archives; scheduled restore-test verifies `verifyRestoreEvery` |
| 36.05 | Cross-region copies + DR runbooks | Copy archives to a second region/target; documented runbook per target |
| 36.06 | Full DR restore flow (new-cluster bootstrap) | `forge restore` end to end against an empty control plane |
| 36.07 | Demo `36-disaster-recovery` + epic gate | Full cluster-loss recovery acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* Application-consistent snapshots for stateful resources reuse epic 30's volume snapshot primitive rather than this epic inventing its own point-in-time mechanism.
* Secrets metadata backups never contain plaintext secret values (epic 32's envelope encryption keeps plaintext out of Control regardless); only encrypted ciphertext and key references are backed up.
* Restore testing runs against an ephemeral scratch environment, not production, to avoid disrupting a running platform.
* RPO/RTO are documented per target class in the `BackupPolicy` status, not a single platform-wide number.

## Open questions

* Does restore reconstruct exact resource IDs (ULIDs) or remap them? Assumption: exact IDs are preserved so `ownerRefs` and external references (deployment history, audit logs) stay valid across a restore.
* Cross-region copy transport when no second region exists (local/bare metal)? Assumption: "cross-region" degrades to "cross-target-directory/disk" locally; real geographic separation is exercised only on the optional cloud-target demo path.
* Restore-test scope — every target on every cycle, or sampled? Assumption: full-target restore test on the `verifyRestoreEvery` cadence to keep the guarantee simple, with cost/duration tuning left as a later optimization.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **36.01 — Backup service skeleton + `BackupPolicy` resource** first: nothing else in the epic can be adapter-tested without the policy resource and schedule runner in place.
