# Epic 18: Managed PostgreSQL

## Status

In progress

## Goal

Provide a product-facing managed PostgreSQL capability (a management plane, not a Postgres replacement) that lets a product request an isolated database: create instance/database/credentials, attach to an application with connection-URL injection via Secrets/Runtime, monitor health, back up and restore, rotate credentials, and enforce deletion protection. Product databases are isolated from the platform's own Control database. Proven by `demos/18-managed-database` and `forge database *` CLI commands.

## Why this epic exists

Real products need databases without hardcoding credentials or touching the platform's internal state store. `specs.md` Step 18 defines a provisioning/management service that issues isolated databases and injects connection URLs securely. This is a prerequisite capability for the capstone product (19).

## Primary code areas

* `services/forge-control/` — management APIs for DB instances/databases/credentials/backups (Control-owned resource model)
* Provisioner (Go) — creates/attaches/backs up product Postgres (separate container(s) or dedicated managed Postgres), invoked by Control
* `contracts/openapi/` — managed-database API additions
* `tools/forge-cli` — `forge database` subcommands
* `demos/18-managed-database/`

## Suggested language

Go for the provisioner (per `specs.md` §4 / Step 18). Control APIs follow Control's stack (Kotlin/Ktor). Provisioner may start as a Go module invoked by Control with a clear extract seam (mirrors the scheduler decision in MASTER_PLAN open question 3).

## Spec references

* `specs.md` → Step 18: Managed PostgreSQL service (features, demo flow, tests, acceptance)
* `specs.md` → Step 10 (Secrets injection), Step 04 (Runtime env injection), Step 02 (Control), Step 09 (Identity)

## Dependencies

* Epics `00`, `01` conventions
* Epic [`02-forge-control`](02-forge-control.md) resource model + APIs (minimum: application/service model from `02.04`, deployments `02.05`)
* Epic [`10-forge-secrets`](10-forge-secrets.md) for secret storage + injection (minimum: secret set + runtime delivery `10.04`)
* Epic [`04-forge-runtime`](04-forge-runtime.md) for env injection into workloads (`04.03`)
* Epic [`03-forge-cli`](03-forge-cli.md) for `forge database` (thin client)

## Out of scope for this epic

* Building a Postgres engine (this manages real Postgres containers/instances)
* HA/replication, connection pooling as a hard requirement (single instance per DB; pooling optional)
* Cross-region / cloud-provider RDS integration (local containers / a dedicated managed Postgres)
* Storing product data in Control's own database (explicitly forbidden — isolation is a core criterion)

## Success demo

```bash
make demo DEMO=18
```

`demos/18-managed-database`: deploy an app needing Postgres → `forge database create` → `forge database attach` → `forge deploy` → app runs migrations + writes data → backup created → restore verifies fixture data.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [18.01](../steps/18-managed-postgresql/18.01-control-apis-provisioner-skeleton.md) | Control APIs + provisioner skeleton | Complete | Resource model + FakeProvisioner seam |
| [18.02](../steps/18-managed-postgresql/18.02-create-instance-db-credentials.md) | Create instance/database/credentials | Complete | LocalProvisioner + Secrets refs; isolated from Control DB |
| [18.03](../steps/18-managed-postgresql/18.03-attach-secrets-runtime-injection.md) | Attach + Secrets/Runtime URL injection | Complete | Attach + URL secret; reconciler injects on deploy |
| [18.04](../steps/18-managed-postgresql/18.04-backup-restore.md) | Backup + restore | Not started | Depends on 18.03 |
| [18.05](../steps/18-managed-postgresql/18.05-rotation-deletion-protection.md) | Credential rotation + deletion protection | Not started | Depends on 18.04 |
| [18.06](../steps/18-managed-postgresql/18.06-cli-demo-and-gate.md) | CLI `forge database *` + demo + gate | Not started | Depends on 18.05; 03 |

## Assumptions

* Product DB instances are **separate Postgres containers** (or databases + dedicated roles on a dedicated "managed" Postgres), never Control's own DB — MASTER_PLAN open question 4.
* Provisioner starts as a Go module/binary invoked by Control; extract to a standalone `services/forge-database` later if needed.
* Backups are `pg_dump`/`pg_basebackup`-based archives stored via Forge Storage (13) or a dedicated backup volume.
* Connection URLs are delivered as secrets (10) and injected at deploy time by Runtime (04); products never receive hardcoded credentials.
* Deletion protection defaults on; deletes require an explicit force + protection disabled.

## Open questions

* Instance topology: one Postgres container per instance vs one shared managed Postgres with per-DB roles. Assumption: one container per instance for strong isolation in local/dev; shared-with-roles documented as an option.
* Backup storage target: Forge Storage vs dedicated volume. Assumption: Forge Storage bucket `db-backups` when available, else a named volume; chosen in `18.04`.
* Provisioner boundary: Control module vs separate service + port. Assumption: module first (no new port); extract seam documented.
* Health monitoring depth: liveness ping vs metrics. Assumption: connection health + basic metrics; deep monitoring via Observe optional.

## Next step to implement

**[18.04](../steps/18-managed-postgresql/18.04-backup-restore.md) — Backup + restore**.
