# 0007. Additive evolution after epic 19

## Status

Accepted (process decision for epics 20–43)

## Context

The standalone-cloud specification arrived while epics `00`–`19` are being implemented
(currently at `N = 50`). Reworking shipped steps, renumbering the queue, or changing
in-flight contracts would interrupt implementation and invalidate completed acceptance
gates.

## Decision

Future work is strictly additive:

1. **Step numbering is append-only.** Steps `1`–`131` are frozen; future work starts at
   `N = 132`. Existing step and epic documents are not edited to accommodate future epics.
2. **No rewrites.** Every future epic states which shipped epic it extends and the
   compatibility rule it uses — a facade over existing endpoints, a new optional field, or
   a new resource kind.
3. **New services start inert.** Discovery, Network, Infrastructure, and Autoscaler are
   introduced disabled or in pass-through mode and flip behind a reversible flag only after
   parity with shipped behaviour is demonstrated.
4. **Local Docker is the CI target.** Cloud-target demos are opt-in
   (`FORGE_DEMO_TARGET=hetzner|aws|azure`) and never gate a merge.
5. **Catalog before materialization.** Epics `20`–`25` have full step files; epics `26`–`43`
   are catalogued in their epic documents and materialized into steps with `PLAN_STEPS.md`
   when milestone M1 completes.

## Consequences

* Implementation of the current queue continues without interruption
* `STEPS.md` and `progress.md` gain a separate, clearly marked future section rather than
  edits to existing rows
* Some future epics will need a refinement pass when their milestone begins — expected, and
  cheaper than planning 24 epics to step depth today
* The compatibility rule is checkable during review: an epic that cannot state one is not
  ready to implement
