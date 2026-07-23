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
| [13](epics/13-forge-storage.md) | Forge Storage | `services/forge-storage` | Rust | Complete (7/7; demo 13 gate) |
| [14](epics/14-forge-models.md) | Forge Models | `services/forge-models` | Python | Complete (7/7; demo 14 gate) |
| [15](epics/15-forge-agents.md) | Forge Agents | `services/forge-agents` | Python | Complete (8/8; demo 15 gate) |
| [16](epics/16-forge-workflows.md) | Forge Workflows | `services/forge-workflows` | Elixir | Complete (7/7; demo 16 gate) |
| [17](epics/17-forge-memory.md) | Forge Memory | `services/forge-memory` | Rust | Complete (6/6; demo 17 gate) |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | control + infra | mixed | Complete (6/6; demo 18 gate) |
| [19](epics/19-full-platform-demo.md) | Full platform demo | `demos/09-full-platform` | — | Complete (6/6; north-star gate) |

## Implementation order

1. Implement planned steps in the global queue in [`MASTER_PLAN.md`](MASTER_PLAN.md) (starts at `01.01`).
2. Use [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md) with exactly one `STEP_ID` per session.
3. Do not start an epic’s first step until its declared cross-epic dependencies’ minimum capabilities exist.

## Planning note

Epics `02`–`19` are fully planned (no stubs). Re-run [`PLAN_STEPS.md`](PLAN_STEPS.md) only to refine a single epic if implementation reveals a missing seam — keep numeric step IDs stable.

---

# Standalone-cloud roadmap (epics 20–43)

Everything below is **future work that starts after epic `19`**. It does not change the
epics above. Target architecture:
[`docs/architecture/standalone-cloud.md`](../architecture/standalone-cloud.md).
Full catalog: [`FUTURE_PLAN.md`](FUTURE_PLAN.md).

```text
M1 — standalone cloud core
20 Declarative resource API
    ↓
21 Forge Discovery ─┬─→ 23 Forge Infrastructure
22 Forge Network ───┘        ↓
                       24 Forge Autoscaler + 25 Scheduling enhancements
                                    ↓
M2 — production platform
26 Registry → 27 Deployment strategies
28 Queue → 29 Database HA → 30 Volumes → 31 Distributed object storage
32 Secrets HA → 33 Policy → 34 DNS + certificates
35 Control-plane HA → 36 Backup + DR → 37 Alerts + incidents
                                    ↓
M3 — global platform
38 AI scheduling → 39 Multi-region → 40 Console
41 Usage + cost → 42 Upgrades → 43 Plugins + extensions
```

## Future epic index

| Epic | Title | Milestone | Extends | Detail status |
|---|---|---|---|---|
| [20](epics/20-declarative-resource-api.md) | Declarative resource API | M1 | 02, 07 | Planned (8 steps, `N = 132`–`139`) |
| [21](epics/21-forge-discovery.md) | Forge Discovery | M1 | 04, 05 | Planned (6 steps, `N = 140`–`145`) |
| [22](epics/22-forge-network.md) | Forge Network | M1 | 04, 08 | Planned (7 steps, `N = 146`–`152`) |
| [23](epics/23-forge-infrastructure.md) | Forge Infrastructure | M1 | 04, 08 | Planned (7 steps, `N = 153`–`159`) |
| [24](epics/24-forge-autoscaler.md) | Forge Autoscaler | M1 | 07, 08, 12 | Complete (8 steps, `N = 160`–`167`) |
| [25](epics/25-scheduling-enhancements.md) | Scheduling enhancements | M1 | 08 | Planned (6 steps, `N = 168`–`173`) |
| [26](epics/26-forge-registry.md) | Forge Registry | M2 | 06 | Catalog |
| [27](epics/27-deployment-strategies.md) | Deployment strategies | M2 | 05, 07 | Catalog |
| [28](epics/28-forge-queue.md) | Forge Queue | M2 | 11 | Catalog |
| [29](epics/29-database-high-availability.md) | Database high availability | M2 | 18 | Catalog |
| [30](epics/30-forge-volumes.md) | Forge Volumes | M2 | 04, 23 | Catalog |
| [31](epics/31-distributed-object-storage.md) | Distributed object storage | M2 | 13 | Catalog |
| [32](epics/32-secrets-high-availability.md) | Secrets high availability | M2 | 10 | Catalog |
| [33](epics/33-forge-policy.md) | Forge Policy | M2 | 02, 09 | Catalog |
| [34](epics/34-dns-and-certificates.md) | DNS and certificates | M2 | 05, 21, 22 | Catalog |
| [35](epics/35-control-plane-high-availability.md) | Control-plane high availability | M2 | 02, 07 | Catalog |
| [36](epics/36-backup-and-disaster-recovery.md) | Backup and disaster recovery | M2 | 13, 18 | Catalog |
| [37](epics/37-alerts-and-incidents.md) | Alerts and incidents | M2 | 12, 15, 16 | Catalog |
| [38](epics/38-ai-infrastructure-scheduling.md) | AI infrastructure scheduling | M3 | 14, 15, 24, 25 | Catalog |
| [39](epics/39-multi-region.md) | Multi-region | M3 | 21, 22, 23 | Catalog |
| [40](epics/40-forge-console.md) | Forge Console | M3 | 09, 20 | Catalog |
| [41](epics/41-usage-quotas-and-cost.md) | Usage, quotas, and cost | M3 | 12, 23, 33 | Catalog |
| [42](epics/42-platform-upgrades.md) | Platform upgrades | M3 | 20, 35 | Catalog |
| [43](epics/43-plugins-and-extensions.md) | Plugins and extensions | M3 | 14, 23, 34 | Catalog |

**Catalog** = step titles listed in the epic doc; materialize into step files with
[`PLAN_STEPS.md`](PLAN_STEPS.md) when milestone M1 is complete.
