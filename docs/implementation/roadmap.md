# Implementation roadmap (epics)

Coarse capability order from `specs.md`. Each epic is planned into multiple atomic steps before coding (except completed foundation).

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
| [00](epics/00-repository-foundation.md) | Repository foundation | root, `infrastructure/`, `docs/` | — | Planned + complete |
| [01](epics/01-runtime-contract.md) | Runtime contract | `demos/`, contracts | polyglot | Stub — needs planning |
| [02](epics/02-forge-control.md) | Forge Control | `services/forge-control` | Kotlin | Stub — needs planning |
| [03](epics/03-forge-cli.md) | Forge CLI | `tools/forge-cli` | Go | Stub — needs planning |
| [04](epics/04-forge-runtime.md) | Forge Runtime | `services/forge-runtime` | Rust | Stub — needs planning |
| [05](epics/05-forge-gateway.md) | Forge Gateway | `services/forge-gateway` | Go | Stub — needs planning |
| [06](epics/06-forge-build.md) | Forge Build | `services/forge-build` | Go | Stub — needs planning |
| [07](epics/07-deployment-reconciliation.md) | Deployment reconciliation | control + runtime | mixed | Stub — needs planning |
| [08](epics/08-multi-node-scheduler.md) | Multi-node scheduler | control + runtime | mixed | Stub — needs planning |
| [09](epics/09-forge-identity.md) | Forge Identity | `services/forge-identity` | Kotlin | Stub — needs planning |
| [10](epics/10-forge-secrets.md) | Forge Secrets | `services/forge-secrets` | Rust | Stub — needs planning |
| [11](epics/11-forge-events.md) | Forge Events | `services/forge-events` | Go | Stub — needs planning |
| [12](epics/12-forge-observe.md) | Forge Observe | `services/forge-observe` | Go | Stub — needs planning |
| [13](epics/13-forge-storage.md) | Forge Storage | `services/forge-storage` | Rust | Stub — needs planning |
| [14](epics/14-forge-models.md) | Forge Models | `services/forge-models` | Python | Stub — needs planning |
| [15](epics/15-forge-agents.md) | Forge Agents | `services/forge-agents` | Python | Stub — needs planning |
| [16](epics/16-forge-workflows.md) | Forge Workflows | `services/forge-workflows` | Elixir | Stub — needs planning |
| [17](epics/17-forge-memory.md) | Forge Memory | `services/forge-memory` | Rust | Stub — needs planning |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | control + infra | mixed | Stub — needs planning |
| [19](epics/19-full-platform-demo.md) | Full platform demo | `demos/` | — | Stub — needs planning |

## Planning order recommendation

Plan the next epic **before** implementing it. Suggested planning queue:

1. `01-runtime-contract` (unblocks all services)
2. `02-forge-control` (expect many steps: domain, API, persistence, deploy API, …)
3. `03-forge-cli` (thin client over Control)
4. Then runtime / gateway / build as separate multi-step epics
