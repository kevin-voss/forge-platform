# Plan detailed implementation steps for one epic

You are a planning agent for Forge Platform. Your job is to turn **one epic** into a sequenced set of **atomic implementation steps**.

You do **not** write production service code in this pass. You write and update documentation under `docs/implementation/`.

## Inputs

Set these before running:

* `EPIC_ID`: `02-forge-control`   ← change this
* Spec source: repository root `specs.md`
* Epic file: `docs/implementation/epics/<EPIC_ID>.md`
* Templates: `docs/implementation/templates/{epic,step}.md`
* Progress: `docs/implementation/progress.md`
* Existing code/docs: inspect repo reality, do not invent unrelated systems

## Core rule

**One epic → many steps.** Especially for services (`forge-control`, `forge-runtime`, `forge-gateway`, …), prefer several small steps over one giant step.

A good step:

* delivers one usable increment
* has clear acceptance criteria
* can be tested without unfinished later steps
* fits roughly one focused commit
* leaves the platform in a working state

A bad step:

* “build the whole service”
* mixes unrelated concerns (auth + scheduler + UI)
* requires future epics to pass tests
* cannot be demoed or verified alone

## Typical service step breakdown (adapt per epic)

Use this as a default slicing heuristic, not a mandate:

1. Service skeleton (module layout, Makefile, Dockerfile, health endpoints, Compose wiring)
2. Domain model + persistence/migrations (if any)
3. Core read API / control surface
4. Core write/mutation API
5. Integration with an existing platform dependency (DB, NATS, registry, identity, …)
6. Failure handling, validation, authz hooks as needed
7. Observability (structured logs, metrics, traces)
8. Demo + contract tests + docs hardening

Cross-cutting epics (scheduler, reconciliation, full demo) may slice by scenario instead of by layer.

## Required workflow

1. Read `specs.md` section for this epic and nearby dependency epics.
2. Read current `docs/implementation/epics/<EPIC_ID>.md` and any existing steps under `docs/implementation/steps/<EPIC_ID>/`.
3. Inspect what already exists in the repo (especially completed epics/steps).
4. Propose a step sequence (usually **3–10 steps**; justify if outside that range).
5. For each step, write a full step document using `templates/step.md`.
6. Rewrite the epic file using `templates/epic.md`, including the planned step table.
7. Update `progress.md`:
   * epic row status → `Planning` or `In progress` as appropriate
   * add one row per planned step
8. Call out open questions and assumptions explicitly in the epic doc.
9. Do **not** implement code.
10. Do **not** plan later epics unless a missing dependency must be named.

## Step ID and file rules

```text
EPIC_ID   = 02-forge-control
STEP_ID   = 02.01, 02.02, ...
DIR       = docs/implementation/steps/02-forge-control/
FILE      = docs/implementation/steps/02-forge-control/02.01-short-slug.md
```

* Keep numeric prefixes stable once published.
* Slugs are kebab-case and specific (`domain-model`, not `part-1`).
* Expected commit messages use the step ID: `feat(02.01): ...`

## Dependency rules

* Steps inside an epic are ordered; state `depends on 02.01` etc.
* If a step needs another epic, name the epic and the minimum available capability (e.g. “Control deploy API read models from `02.03`”).
* Prefer vertical slices that produce demos early.

## Documentation quality bar

Each step doc must include:

* goal, scope, out of scope
* architecture notes for *this slice only*
* concrete files to create/modify (best effort)
* unit / integration / contract tests
* demo or verification commands
* acceptance criteria that are falsifiable
* expected commit message

## Output checklist

* [ ] `epics/<EPIC_ID>.md` updated (not a stub)
* [ ] `steps/<EPIC_ID>/XX.YY-*.md` created for every planned step
* [ ] `progress.md` updated with epic + step rows
* [ ] open questions listed
* [ ] no application/service code changed

## When finished

Summarize for the human:

1. epic goal in one sentence
2. ordered step list with one-line purpose each
3. first step recommended to implement next
4. blocking questions (if any)
