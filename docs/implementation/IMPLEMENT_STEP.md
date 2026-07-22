# Implement one Forge Platform step

You are implementing **exactly one atomic step**.

## Inputs

Set these before running:

* `STEP_ID`: `00.01`   ← change this
* Step document: `docs/implementation/steps/<epic-dir>/<STEP_ID>-*.md`
* Epic document: `docs/implementation/epics/<epic-dir>.md`
* Progress file: `docs/implementation/progress.md`
* Spec source: `specs.md` (context only; the step doc wins on scope)

## Required workflow

1. Read the step document end-to-end.
2. Read its parent epic and confirm dependencies are satisfied.
3. Inspect existing architecture and conventions in the repo.
4. Implement **only** this step’s scope.
5. Run the tests/demo commands listed in the step doc (and root checks if specified).
6. Fix failures until acceptance criteria pass.
7. Update docs touched by the step (service README, architecture, ADR if needed).
8. Update `progress.md` (step + epic rollup status).
9. Review `git diff`.
10. Create **one** Git commit using the step’s expected message.
11. Leave a clean working tree.

## Must not

* implement later steps in the same epic
* start a different epic
* silently weaken or delete tests
* skip failing tests
* rewrite unrelated services
* commit secrets or local runtime data
* claim completion while acceptance criteria fail

## Definition of done

All of:

* step acceptance criteria pass
* applicable items from `specs.md` “Definition of done for every implementation step”
* docs + progress updated
* one commit, clean tree
