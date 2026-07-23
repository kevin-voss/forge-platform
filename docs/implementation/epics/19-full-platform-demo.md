# Epic 19: Full AI-native platform demo (capstone)

## Status

In progress

## Goal

Prove the complete Forge ecosystem works together by building a small polyglot incident-management product (Go API, Kotlin admin, Rust log worker, Python classification, Elixir notifications) deployed through the platform, then demonstrate the north-star recovery scenario: a deliberately broken release is detected via readiness failure, an event starts a workflow, an agent (using memory of a similar historical incident) diagnoses the problem from real telemetry, a human approves rollback, the workflow rolls back, a final report is stored, and the product becomes healthy again — all runnable and verifiable with one command each.

## Why this epic exists

This is the acceptance of the entire platform (`specs.md` Step 19). It exercises every prior epic through documented contracts only and demonstrates the AI-native operations loop end to end.

## Demo folder naming (IMPORTANT — read carefully)

* The **capstone lives in `demos/09-full-platform`** — the thematic §3 north-star name — even though this is epic `19`. It is NOT `demos/19-*`.
* The **Identity epic demo remains `demos/09-platform-identity`** (epic `09`). These are two DIFFERENT folders that both carry the numeral `09` for different reasons (thematic north-star vs epic index).
* Do not rename either folder. Do not merge them. Epic 09's gate is `demos/09-platform-identity`; epic 19's gate is `demos/09-full-platform`.

| Purpose | Folder | Owning epic/step |
|---|---|---|
| Identity epic demo | `demos/09-platform-identity` | `09.08` |
| Capstone full-platform demo | `demos/09-full-platform` | `19.06` |

## Primary code areas

* `demos/09-full-platform/` — capstone Compose, orchestration, acceptance suite, docs
* `demos/09-full-platform/product/` — the polyglot incident-management product (five services)
* Product `forge.yaml`, workflow definitions, agent config, fixtures

## Suggested language

Polyglot product: Go, Kotlin, Rust, Python, Elixir (per `specs.md` Step 19 architecture). No new platform language.

## Spec references

* `specs.md` → Step 19: Full AI-native platform demo (architecture, main scenario, tests, acceptance)
* `specs.md` → §3 thematic demos (capstone name `09-full-platform`), §8 epic demo paths
* All prior epic specs (Steps 02–18)

## Dependencies

All prior epics sufficiently complete (minimum capabilities named per step):

* [`02`](02-forge-control.md) Control, [`03`](03-forge-cli.md) CLI, [`04`](04-forge-runtime.md) Runtime, [`05`](05-forge-gateway.md) Gateway, [`06`](06-forge-build.md) Build, [`07`](07-deployment-reconciliation.md) Reconciliation/rollback, [`08`](08-multi-node-scheduler.md) Scheduler
* [`09`](09-forge-identity.md) Identity, [`10`](10-forge-secrets.md) Secrets, [`11`](11-forge-events.md) Events, [`12`](12-forge-observe.md) Observe
* [`13`](13-forge-storage.md) Storage, [`14`](14-forge-models.md) Models, [`15`](15-forge-agents.md) Agents, [`16`](16-forge-workflows.md) Workflows, [`17`](17-forge-memory.md) Memory, [`18`](18-managed-postgresql.md) Managed Postgres

## Out of scope for this epic

* New platform capabilities (all live in epics 02–18; this epic only integrates)
* Rewriting platform services
* Non-`specs.md` products
* Production hardening beyond the acceptance scenario

## Success demo

```bash
make demo DEMO=09-full-platform    # or the documented capstone selector
# then
make demo-accept DEMO=09-full-platform
```

One command starts the entire demo; one command runs the acceptance suite proving the broken-release detection → diagnosis → approval → rollback → recovery loop.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [19.01](../steps/19-full-platform-demo/19.01-polyglot-product-scaffold.md) | Polyglot sample product | Complete | Go/Kotlin/Rust/Python/Elixir services under `product/` |
| [19.02](../steps/19-full-platform-demo/19.02-deploy-path.md) | Deploy path: Build→Runtime→Gateway→Events | Complete | Capstone compose/deploy.sh; Gateway routes; incident.created |
| [19.03](../steps/19-full-platform-demo/19.03-identity-secrets-observe-storage-db.md) | Identity, Secrets, Observe, Storage, managed DB | Complete | Identity roles; Secrets+DB inject; Storage; Observe product trace |
| [19.04](../steps/19-full-platform-demo/19.04-models-agents-memory.md) | Models + Agents + Memory for diagnosis | Complete | Memory seed + investigator diagnosis loop |
| [19.05](../steps/19-full-platform-demo/19.05-failure-injection-workflow.md) | Failure injection + Workflows approval/rollback | Complete | CAPSTONE_BREAK + incident-response approve/deny/resume |
| [19.06](../steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) | `demos/09-full-platform` acceptance suite + docs | Not started | Depends on 19.05; north-star gate |

## Assumptions

* Capstone folder is `demos/09-full-platform` (thematic id `09`); Identity demo stays `demos/09-platform-identity` (separate folder).
* The product is intentionally minimal but real: five services communicating via Gateway/Events, backed by a managed Postgres and using Storage/Models/Agents/Memory/Workflows.
* CI determinism uses fake Models/Agents tool modes; local runs may use real small models.
* A "broken release" is a v2 image whose readiness check fails deterministically (feature flag/env), enabling reproducible detection.
* The acceptance suite runs against the full Compose stack (or a documented CI subset if full-stack image build time is prohibitive — see Open questions).

## Open questions

* Full stack vs subset in CI: can CI run all ~14 services + product, or a curated subset? Assumption: full stack locally; CI runs a documented subset gate (core loop) with the full suite runnable on demand.
* Capstone `make` selector: `DEMO=09-full-platform` string vs a dedicated `make demo-full` target. Assumption: support a string selector and document a convenience alias.
* Scheduler (08) participation: single-node vs multi-node in the capstone. Assumption: single-node by default; multi-node reschedule is an optional extended assertion.
* Where the final report is stored: Storage bucket + Events emission. Assumption: both — report artifact in Storage, completion event on Events.

## Next step to implement

**[19.06](../steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) — `demos/09-full-platform` acceptance suite + docs** (depends on 19.05).
