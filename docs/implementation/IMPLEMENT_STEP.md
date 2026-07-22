# Implement one Forge Platform step

You are implementing **exactly one atomic step**.

## Inputs

Change **only** this number:

```text
N = 1
```

Lookup: open [`STEPS.md`](STEPS.md) and find the row for that `N`.  
That row’s step document is the source of truth for scope.

Do **not** ask the human for `01.01`-style ids. Resolve them yourself from `STEPS.md` / the step file’s “Global sequence (N)” section.

Also use:

* Epic document linked from the step file
* [`progress.md`](progress.md) — mark this `N` complete when done
* `specs.md` — context only; the step doc wins on scope

## Required workflow

1. Resolve `N` → step file via [`STEPS.md`](STEPS.md).
2. Read the step document end-to-end.
3. Read its parent epic and confirm dependencies are satisfied.
4. Inspect existing architecture and conventions in the repo.
5. Implement **only** this step’s scope.
6. Run the tests/demo commands listed in the step doc (and root checks if specified).
7. Fix failures until acceptance criteria pass.
8. Update docs touched by the step (service README, architecture, ADR if needed).
9. Update `progress.md` for this `N` (and epic rollup if the epic finishes).
10. Review `git diff`.
11. Create **one** Git commit using the step’s expected message.
12. Leave a clean working tree.

## Must not

* implement a different `N` or later steps
* start a different epic beyond what this step requires
* silently weaken or delete tests
* skip failing tests
* rewrite unrelated services
* commit secrets or local runtime data
* claim completion while acceptance criteria fail

## Definition of done

All of:

* step acceptance criteria pass
* applicable items from `specs.md` “Definition of done for every implementation step”
* docs + progress updated for this `N`
* one commit, clean tree

## Copy-paste prompt

```text
Follow docs/implementation/IMPLEMENT_STEP.md exactly.

N = 1
```
