# Implementation system

Forge is built as **epics** made of **atomic steps**.

```text
specs.md                     ← product vision + coarse roadmap
docs/implementation/
├── MASTER_PLAN.md           ← full-ecosystem step catalog + global queue
├── STEPS.md                 ← N = 1, 2, 3… implement queue (change only N)
├── roadmap.md               ← epic map + dependencies
├── progress.md              ← status board (epics + steps)
├── PLAN_STEPS.md            ← prompt: break an epic into steps
├── IMPLEMENT_STEP.md        ← prompt: implement exactly one step
├── templates/
│   ├── epic.md
│   └── step.md
├── epics/                   ← one file per epic (capability)
└── steps/
    └── <epic-id>-<slug>/    ← many step files per epic
        ├── XX.01-....md
        └── XX.02-....md
```

## Model

| Layer | ID example | Meaning |
|---|---|---|
| Epic | `02-forge-control` | A capability from the roadmap; may span many commits |
| Step | `02.01` | One shippable increment with tests, docs, and one commit |

Rules:

1. An epic is **not** implemented in one go unless it is truly tiny.
2. Each service epic (Control, Runtime, Gateway, …) is expected to become **multiple steps**.
3. A step must be independently demoable or verifiable.
4. Later steps must not be required for earlier steps to pass.
5. Planning writes/updates files under `epics/` and `steps/`; implementing changes code.

## Workflow

### 1. Plan (detail an epic into steps)

Use [`PLAN_STEPS.md`](PLAN_STEPS.md) with one epic ID, e.g. `02-forge-control`.

For the full ecosystem catalog, see [`MASTER_PLAN.md`](MASTER_PLAN.md).

Output:

* updated epic README-style doc in `epics/`
* one step file per increment under `steps/<epic>/`
* rows added to `progress.md`

### 2. Implement (one step only)

Use [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md) with **`N = 1`** (then `2`, `3`, …). Lookup: [`STEPS.md`](STEPS.md).

### 3. Track

Update [`progress.md`](progress.md) after planning and after each step completes.

## Current state

| Epic | Status | Planned steps |
|---|---|---|
| [00-repository-foundation](epics/00-repository-foundation.md) | Complete | `00.01` |
| [01-runtime-contract](epics/01-runtime-contract.md) | Planning | `01.01`–`01.07` |
| [02-forge-control](epics/02-forge-control.md) | Planning | `02.01`–`02.08` |
| [03-forge-cli](epics/03-forge-cli.md) | Planning | `03.01`–`03.06` |
| [04-forge-runtime](epics/04-forge-runtime.md) | In progress | `04.01`–`04.08` |
| [05-forge-gateway](epics/05-forge-gateway.md) | Planning | `05.01`–`05.07` |
| [06-forge-build](epics/06-forge-build.md) | Planning | `06.01`–`06.07` |
| [07-deployment-reconciliation](epics/07-deployment-reconciliation.md) | Planning | `07.01`–`07.06` |
| [08-multi-node-scheduler](epics/08-multi-node-scheduler.md) | Planning | `08.01`–`08.06` |
| [09-forge-identity](epics/09-forge-identity.md) | Planning | `09.01`–`09.08` |
| [10-forge-secrets](epics/10-forge-secrets.md) | Planning | `10.01`–`10.07` |
| [11-forge-events](epics/11-forge-events.md) | Planning | `11.01`–`11.07` |
| [12-forge-observe](epics/12-forge-observe.md) | Planning | `12.01`–`12.07` |
| [13-forge-storage](epics/13-forge-storage.md) | Planning | `13.01`–`13.07` |
| [14-forge-models](epics/14-forge-models.md) | Planning | `14.01`–`14.07` |
| [15-forge-agents](epics/15-forge-agents.md) | Planning | `15.01`–`15.08` |
| [16-forge-workflows](epics/16-forge-workflows.md) | Planning | `16.01`–`16.07` |
| [17-forge-memory](epics/17-forge-memory.md) | Planning | `17.01`–`17.06` |
| [18-managed-postgresql](epics/18-managed-postgresql.md) | Planning | `18.01`–`18.06` |
| [19-full-platform-demo](epics/19-full-platform-demo.md) | Planning | `19.01`–`19.06` |

**Next:** `N = 1` via [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md) — lookup in [`STEPS.md`](STEPS.md).

Queue: [`STEPS.md`](STEPS.md) (`N = 1` … `N = 131`). Catalog: [`MASTER_PLAN.md`](MASTER_PLAN.md).

## Naming

```text
Human prompt:  N = 8                          ← only number you change
Lookup:        STEPS.md row for N
Epic file:     docs/implementation/epics/02-forge-control.md
Step file:     docs/implementation/steps/02-forge-control/02.01-….md
Commit:        feat(02.01): …                 ← from the step doc (internal)
```
