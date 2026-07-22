# Epic 29: Database high availability

## Status

Planning

## Milestone

**M2 — Production platform.** HA database topology is one of the named M2 capabilities: a single-instance managed Postgres (epic 18) is a development convenience, not a production guarantee. This epic closes that gap in four explicit stages.

## Goal

Extend Forge's managed PostgreSQL capability from a single instance into a topology-aware, self-healing database platform: (1) primary + persistent volume + PgBouncer + scheduled logical backups + restore + deletion protection, (2) streaming standby, replication monitoring, and automated failover behind a stable writer endpoint with tracked RPO, (3) read replicas behind a stable reader endpoint with read load balancing and replica-lag monitoring and autoscaling, (4) point-in-time recovery, WAL archiving, cross-region backup copies, controlled major-version upgrades, online storage expansion, and maintenance windows. A `Database` resource gains `topology.mode: high-availability`, `synchronousStandbys`, `readReplicas.min/max`, `storage.expandable`, and `backups.pointInTimeRecovery`. Proven by `demos/29-database-failover`: kill the primary, watch a standby promote, the writer endpoint move, the old primary fenced, and a replacement standby provisioned — with zero data loss for the synchronous standby.

## Why this epic exists

Epic 18 deliberately shipped one Postgres instance per database to prove provisioning, credential injection, backup, and rotation without replication complexity. A database is not a stateless workload: losing the only instance loses data, and "restart the container" is not a failover story. This epic is the platform's answer — a topology-aware controller that treats a database as a stateful, replicated system with explicit safety rules, not another autoscaled container.

## Relationship to shipped epics

Extends **epic 18 — Managed PostgreSQL**. `topology.mode: high-availability` is a new, additive field on the existing `Database` spec; the default remains `standalone`, so every database created under epic 18's shipped behavior (`18.02`) keeps running exactly as-is, unmodified and unmigrated, unless an operator opts a database into HA. The `18.04` backup/restore contract and the `18.05` rotation/deletion-protection contract are extended with new fields (`pointInTimeRecovery`, replica-aware rotation) rather than replaced.

## Primary code areas

* `services/forge-data/` — new service (port `4116`), extracted from epic 18's Control-invoked Go provisioner now that topology management needs its own control loop
* `services/forge-control/` — `Database` resource API additions (topology, replica, backup fields)
* `services/forge-secrets/` — writer/reader connection-URL rotation on failover
* `demos/29-database-failover/`

## Suggested language

Go — continues epic 18's provisioner language. The provisioner is extracted from a Control-invoked module into its own service now that continuous replication monitoring and failover detection need an independent control loop, mirroring the module-to-service extraction pattern epic 08 documents for the scheduler.

## Spec references

* `docs/architecture/standalone-cloud.md` § Database high availability
* `specs.md` → Step 18: Managed PostgreSQL service
* [`epics/18-managed-postgresql.md`](18-managed-postgresql.md)

## Dependencies

* [`18-managed-postgresql`](18-managed-postgresql.md) — single-instance baseline this epic extends
* `21-forge-discovery` — stable writer/reader endpoint updates on failover (future M1 epic)
* [`10-forge-secrets`](10-forge-secrets.md) — connection-URL rotation delivery
* `31-distributed-object-storage` / [`13-forge-storage`](13-forge-storage.md) — backup and WAL-archive target
* `20-declarative-resource-api` — additive `Database.spec.topology` conventions

## Out of scope for this epic

* Multi-region database topology (cross-region active-active is epic 39)
* Automatic upgrade of Postgres extensions beyond core version upgrades
* Building a custom replication protocol — this epic uses Postgres's own streaming replication
* Requiring a cloud-managed database (RDS, Azure Database for PostgreSQL, Hetzner managed DB) — those remain optional adapters at most

## Portability contract

A product manifest declares only `database: {type: postgres, plan: standard | ha}` — never an RDS instance class, an Azure Database for PostgreSQL SKU, or a Hetzner managed-database plan id. HA topology is built from primary and standby Postgres containers/VMs the platform provisions itself on whatever compute primitive the target offers, so the same manifest produces the same primary/standby/replica topology on local Docker, bare metal, Hetzner, AWS EC2, and Azure VMs. A managed cloud database service may be wired in only as an optional adapter behind the same `Database` API — never required to pass `demos/29-database-failover`, which must run entirely on self-provisioned Postgres.

**Data-safety rules (non-negotiable):**

* Databases are **never cascade-deleted**: the finalizer blocks deletion until an explicit `forge database delete --force` with deletion protection first disabled, exactly as epic 18 already requires.
* Failover requires **confirmed** primary failure (multiple missed health checks plus a connectivity check from more than one observer), never a single missed heartbeat, before promotion is triggered.
* The demoted/failed primary is **fenced** (connections terminated, writes refused) before the promoted standby is announced as the new writer, to prevent split-brain.
* Backups are **validated**, not presumed valid: a `verifyRestoreEvery` scheduled job actually restores a backup and checks it, matching the model epic 36's `BackupPolicy` will later apply platform-wide.
* Storage expansion is online and additive only — never a destructive resize.

## Success demo

```bash
make demo DEMO=29
```

```text
Database invoice-db: topology.mode high-availability, synchronousStandbys 1,
                      readReplicas.min 1, readReplicas.max 3
  → primary + 1 sync standby + 1 read replica provisioned; PgBouncer fronts the writer endpoint
  → primary process killed
  → replication monitor confirms failure (multiple checks, multiple observers) → standby promoted
  → Discovery updates the stable writer endpoint → old primary fenced
  → application reconnects via PgBouncer with zero data loss (RPO 0 against the sync standby)
  → replacement standby provisioned automatically → topology restored to primary + standby + replica
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 29.01 | HA-mode baseline: volume + PgBouncer + scheduled backups + deletion protection | `topology.mode: high-availability` opt-in, extends `18.02`–`18.05` |
| 29.02 | Streaming standby + replication monitoring | Standby provisioning, lag/health tracking |
| 29.03 | Automated failover + stable writer endpoint + RPO tracking | Confirmed-failure detection, promotion, endpoint update |
| 29.04 | Fencing + replacement standby provisioning | Prevent split-brain; restore topology automatically |
| 29.05 | Read replicas + stable reader endpoint + read load balancing | Read scaling with lag-aware routing |
| 29.06 | Replica-lag monitoring + read-replica autoscaling | Add/remove replicas on read load and lag thresholds |
| 29.07 | PITR + WAL archiving + cross-region backup copies | Point-in-time restore; durable WAL archive |
| 29.08 | Controlled major-version upgrades + online storage expansion + maintenance windows | Safe upgrade path; non-destructive growth |
| 29.09 | Demo `29-database-failover` + gate | Kill primary → promote → fence → reprovision, zero data loss |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* HA mode is opt-in per database (`topology.mode: high-availability`); `standalone` remains the default and is functionally identical to what epic 18 ships today.
* Replication uses native Postgres streaming replication (physical), not logical replication, for the primary/standby pair; logical replication is reserved for cross-region backup copies if needed.
* The writer/reader endpoint is a stable DNS-ish name resolved through `21-forge-discovery`'s zone, not a raw IP the application ever hardcodes.
* PgBouncer sits in front of every HA database regardless of whether pooling is otherwise required, so endpoint changes on failover do not require an application reconnect storm.
* One backup provisioner extraction (`services/forge-data`) serves both standalone (epic 18) and HA (epic 29) databases; it does not fork into two codebases.

## Open questions

* What counts as "confirmed" primary failure — how many checks, from how many observers, over what window? **Assumption:** at least two independent health-check failures from at least two distinct observers (e.g., the `forge-data` controller and a standby's own connection attempt) within a 10-second window before promotion begins.
* Is failover fully automatic, or does it require an operator acknowledgment for production databases? **Assumption:** automatic by default with an optional `failover.requireApproval: true` field for databases that want a human gate, deferring the full approval workflow model to epic 37.
* Do read replicas serve strongly-consistent reads, or only eventually-consistent reads? **Assumption:** eventually consistent with replica-lag exposed as a status field; applications needing strong reads target the writer endpoint explicitly.
* Where do WAL archives and cross-region backup copies live? **Assumption:** Forge Storage / distributed object storage (epic 31) buckets, matching epic 18's existing assumption that backups live in Forge Storage rather than a bespoke volume.

## Next step to implement

**29.01 — HA-mode baseline: volume + PgBouncer + scheduled backups + deletion protection** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `29.01-ha-baseline-volume-pgbouncer-backups.md` and assign its `N`).
