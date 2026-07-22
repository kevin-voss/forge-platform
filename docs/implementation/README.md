# Implementation system

Forge is built as **epics** made of **atomic steps**.

```text
specs.md                     ← product vision + coarse roadmap
docs/implementation/
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

Output:

* updated epic README-style doc in `epics/`
* one step file per increment under `steps/<epic>/`
* rows added to `progress.md`

### 2. Implement (one step only)

Use [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md) with one step ID, e.g. `02.01`.

### 3. Track

Update [`progress.md`](progress.md) after planning and after each step completes.

## Current state

| Epic | Status | Planned steps |
|---|---|---|
| [00-repository-foundation](epics/00-repository-foundation.md) | Complete | `00.01` |
| 01–19 | Not planned in detail | stubs only — run `PLAN_STEPS` per epic |

## Naming

```text
Epic file:   docs/implementation/epics/02-forge-control.md
Step dir:    docs/implementation/steps/02-forge-control/
Step file:   docs/implementation/steps/02-forge-control/02.01-domain-model.md
Step ID:     02.01
Commit:      feat(02.01): add forge-control domain model
```
