# Implementation roadmap (epics)

Coarse capability order from `specs.md`. Each epic is planned into multiple atomic steps before coding (except completed foundation).

Full step catalog + global queue: [`MASTER_PLAN.md`](MASTER_PLAN.md).

```text
00 Repository foundation
    ↓
01 Runtime contract
    ↓
02 Forge Control ─────────────┐
    ↓                         │
03 Forge CLI  ← depends on Control API surface
    ↓
04 Forge Runtime
    ↓
05 Forge Gateway
    ↓
06 Forge Build
    ↓
07 Deployment reconciliation
    ↓
08 Multi-node scheduler
    ↓
09 Forge Identity
    ↓
10 Forge Secrets
    ↓
11 Forge Events
    ↓
12 Forge Observe
    ↓
13 Forge Storage
    ↓
14 Forge Models
    ↓
15 Forge Agents
    ↓
16 Forge Workflows
    ↓
17 Forge Memory
    ↓
18 Managed PostgreSQL
    ↓
19 Full platform demo
```

## Epic index

| Epic | Title | Primary code area | Lang (suggested) | Detail status |
|---|---|---|---|---|
| [00](epics/00-repository-foundation.md) | Repository foundation | root, `infrastructure/`, `docs/` | — | Planned + complete (`00.01`) |
| [01](epics/01-runtime-contract.md) | Runtime contract | `demos/`, contracts | polyglot | Planned (7 steps) |
| [02](epics/02-forge-control.md) | Forge Control | `services/forge-control` | Kotlin | Complete (8/8 steps; demo 02 gate passed) |
| [03](epics/03-forge-cli.md) | Forge CLI | `tools/forge-cli` | Go | Complete (6/6 steps; demo 03 gate passed) |
| [04](epics/04-forge-runtime.md) | Forge Runtime | `services/forge-runtime` | Rust | Complete (8/8 steps; demo 04 gate passed) |
| [05](epics/05-forge-gateway.md) | Forge Gateway | `services/forge-gateway` | Go | Complete (7/7 steps; demo 05 gate passed) |
| [06](epics/06-forge-build.md) | Forge Build | `services/forge-build` | Go | Complete (7/7 steps; demo 06 gate passed) |
| [07](epics/07-deployment-reconciliation.md) | Deployment reconciliation | control + runtime | mixed | Planned (6 steps) |
| [08](epics/08-multi-node-scheduler.md) | Multi-node scheduler | control + runtime | mixed | Planned (6 steps) |
| [09](epics/09-forge-identity.md) | Forge Identity | `services/forge-identity` | Kotlin | Planned (8 steps) |
| [10](epics/10-forge-secrets.md) | Forge Secrets | `services/forge-secrets` | Rust | Planned (7 steps) |
| [11](epics/11-forge-events.md) | Forge Events | `services/forge-events` | Go | Planned (7 steps) |
| [12](epics/12-forge-observe.md) | Forge Observe | `services/forge-observe` | Go | Planned (7 steps) |
| [13](epics/13-forge-storage.md) | Forge Storage | `services/forge-storage` | Rust | Planned (7 steps) |
| [14](epics/14-forge-models.md) | Forge Models | `services/forge-models` | Python | Planned (7 steps) |
| [15](epics/15-forge-agents.md) | Forge Agents | `services/forge-agents` | Python | Planned (8 steps) |
| [16](epics/16-forge-workflows.md) | Forge Workflows | `services/forge-workflows` | Elixir | Planned (7 steps) |
| [17](epics/17-forge-memory.md) | Forge Memory | `services/forge-memory` | Rust | Planned (6 steps) |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | control + infra | mixed | Planned (6 steps) |
| [19](epics/19-full-platform-demo.md) | Full platform demo | `demos/09-full-platform` | — | Planned (6 steps) |

## Implementation order

1. Implement planned steps in the global queue in [`MASTER_PLAN.md`](MASTER_PLAN.md) (starts at `01.01`).
2. Use [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md) with exactly one `STEP_ID` per session.
3. Do not start an epic’s first step until its declared cross-epic dependencies’ minimum capabilities exist.

## Planning note

Epics `02`–`19` are fully planned (no stubs). Re-run [`PLAN_STEPS.md`](PLAN_STEPS.md) only to refine a single epic if implementation reveals a missing seam — keep numeric step IDs stable.
