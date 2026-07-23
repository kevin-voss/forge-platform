# Steps for epic 18-managed-postgresql

Epic: [`../../epics/18-managed-postgresql.md`](../../epics/18-managed-postgresql.md) · Status: **In progress**

Managed PostgreSQL provisioning (Control + provisioner, demo `demos/18-managed-database`). Product DBs are isolated from Control's own database.

| Step | Title | Status | Depends on |
|---|---|---|---|
| [18.01](18.01-control-apis-provisioner-skeleton.md) | Control APIs + provisioner skeleton | Complete | 02 |
| [18.02](18.02-create-instance-db-credentials.md) | Create instance/database/credentials | Complete | 18.01, 10 |
| [18.03](18.03-attach-secrets-runtime-injection.md) | Attach + Secrets/Runtime URL injection | Complete | 18.02, 10, 04 |
| [18.04](18.04-backup-restore.md) | Backup + restore | Complete | 18.03, 13 |
| [18.05](18.05-rotation-deletion-protection.md) | Credential rotation + deletion protection | Not started | 18.04 |
| [18.06](18.06-cli-demo-and-gate.md) | CLI `forge database *` + demo + gate | Not started | 18.05, 03 |

Next to implement: **18.05**.
