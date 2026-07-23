# Demo 18 — Managed PostgreSQL

Epic gate for managed PostgreSQL: provision an isolated product database with
`forge database`, attach it to an application, deploy so Runtime injects
`DATABASE_URL` from Secrets (no hardcoded credentials), then backup and restore
a known fixture row.

## One command

```bash
make demo DEMO=18
```

Exit `0` means the acceptance suite passed.

## Flow

1. `forge database create main` — LocalProvisioner starts an isolated Postgres
   container (not Control's database) and creates a least-privilege database/role.
2. `forge database attach main --app backend --env-var DATABASE_URL` — Control
   stores a composed connection URL in Secrets (`secretRef` only in responses).
3. `forge deployment create` — reconciler injects `DATABASE_URL` into the workload.
4. Demo app migrates, writes fixture `demo18-fixture=managed-db-ok`.
5. `forge database backup main` — on-demand `pg_dump` with checksum.
6. Fixture is cleared, then `forge database restore` recovers it.
7. `forge database rotate` confirms credential rotation (18.05).

## Layout

```text
demos/18-managed-database/
├── compose.yaml     # Control LocalProvisioner + docker.sock overlay
├── app/             # tiny Python app (DATABASE_URL only)
├── run.sh
├── acceptance.sh
└── README.md
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_DB_PROVISIONER` | `local` | Real Docker Postgres per instance |
| `FORGE_DB_ENDPOINT_HOST` | `host.docker.internal` | Host workloads use to reach published ports |
| `FORGE_DB_MANAGED_NETWORK` | `forge-net` | Network for product Postgres containers |
| `FORGE_AUTH_MODE` | `enforce` | Identity + Secrets authz |

## Assertions

* Product DB host is not Control's Postgres
* App source has no hardcoded credentials
* `/db-status` never echoes `DATABASE_URL`
* Backup checksum present; restore recovers the fixture
* CLI drives create/attach/list/backup/restore/rotate against Control APIs
