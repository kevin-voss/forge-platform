# Epic 27: Deployment strategies

## Status

Planning

## Milestone

**M2 — Production platform.** Rolling updates (epic 07) are enough for a demo; production traffic needs staged exposure with automated safety gates. This epic gives every M2 deployment the option of blue-green, canary, and shadow strategies without touching the rolling-update path already shipped.

## Goal

Generalize the epic 07 reconciler's single hard-coded rolling strategy into a pluggable strategy model: blue-green, canary with configurable traffic-percentage steps, shadow traffic, manual and automatic promotion, deployment freeze windows, release approval, and regional rollout coordination. A new `Revision` resource tracks each deployed version's traffic weight and health. Canary analysis reads Observe metrics (`errorRateMaximum`, `p95LatencyMaximum`); a gate breach pauses the rollout and then rolls back automatically, the same automatic-rollback guarantee epic 07 already provides for rolling updates. Proven by `demos/27-canary-rollout`.

## Why this epic exists

Epic 07 shipped exactly one strategy — rolling, one replica at a time — because that was sufficient to prove the reconciler's desired/actual model and automatic rollback. Real production rollouts want to expose a new version to a small percentage of traffic, watch real metrics, and only then commit — with a human or an automated gate deciding to proceed. Building this as a strategy plug-in point rather than a rewrite keeps every already-shipped rolling deployment working exactly as it does today.

## Relationship to shipped epics

Extends **epic 07 — Deployment reconciliation**. `07.03`'s rolling algorithm becomes one case (`strategy: rolling`, the default) of a generalized `DeploymentStrategy` interface the reconciler dispatches on; the `strategy` field is a new, additive, optional field on the existing Deployment spec — omitting it preserves exactly today's rolling behavior. `07.04`'s automatic-rollback-on-timeout logic is reused verbatim as the fallback for every strategy, not reimplemented per strategy. Also extends **epic 05 — Forge Gateway**: weighted routing is an additive capability alongside `05.04`'s existing health-aware upstream selection, not a replacement of it.

## Primary code areas

* `services/forge-control/` — reconcile module: `DeploymentStrategy` interface, `Revision` resource, canary analysis loop (extends `07.03`–`07.05`)
* `services/forge-gateway/` — weighted traffic-percentage routing (extends `05.04`)
* `services/forge-observe/` — metrics queries used as canary analysis gates (consumes epic 12 unchanged)
* `demos/27-canary-rollout/`
* `contracts/openapi/` — `Revision` resource + strategy fields

## Suggested language

Kotlin — stays inside the Forge Control reconcile module started in `07.01`, per the same extract-seam reasoning as epic 08's scheduler. Port `4114` (`forge-deploy`) is reserved for the day the reconciler is extracted into a standalone service; this epic does not require that extraction.

## Spec references

* `docs/architecture/standalone-cloud.md` § Deployment strategies
* `specs.md` → Step 07: Reconciliation and deployment controller
* `specs.md` → Step 12: Forge Observe (metrics consumed as analysis gates)
* [`epics/07-deployment-reconciliation.md`](07-deployment-reconciliation.md) → `07.03` rolling update, `07.04` automatic rollback
* [`epics/05-forge-gateway.md`](05-forge-gateway.md) → `05.04` health-aware upstream selection

## Dependencies

* [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — reconciler baseline this epic generalizes
* [`05-forge-gateway`](05-forge-gateway.md) — weighted routing addition
* `12-forge-observe` — metrics source for canary analysis gates
* `21-forge-discovery` — revision-aware endpoints (future M1 epic) so Gateway can address a specific revision's replicas
* `20-declarative-resource-api` — `Revision` resource conventions

## Out of scope for this epic

* Multi-region rollout orchestration (coordinating waves across regions is epic 39)
* Cost-aware rollout scheduling (epic 41)
* A visual approval UI (release approval here is API + CLI only; the console is epic 40)
* Inventing a new metrics backend — canary analysis strictly queries Observe's existing query surface

## Portability contract

A product manifest declares `strategy: canary` and step/gate values only — never a load-balancer ARN, target-group weight, or provider traffic-shaping primitive (no AWS ALB weighted target groups, no Azure Traffic Manager profile). Forge Gateway implements weighted routing itself, so canary and blue-green behave identically on local Docker, bare metal, Hetzner, AWS, and Azure — the percentage split is enforced by Gateway's own proxy layer, not by a cloud load balancer.

## Success demo

```bash
make demo DEMO=27
```

```text
deploy invoice-api v2, strategy: canary, steps: [10%, 50%, 100%]
  analysis: errorRateMaximum 1%, p95LatencyMaximum 250ms
  → Revision v2 created; Gateway shifts 10% of traffic to it
  → Observe reports error rate 4% (> 1%) → canary paused → automatic rollback to v1 (07.04 guarantee reused)
  operator fixes the bug, redeploys v2
  → 10% passes analysis → 50% passes analysis → 100% → v2 promoted, v1 retired
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 27.01 | `Revision` resource + strategy field on Deployment spec | Additive field, default `rolling`; no change to existing behavior |
| 27.02 | Blue-green strategy | Full parallel environment, instant traffic cutover |
| 27.03 | Canary + traffic-percentage rollout via Gateway weighted routing | Staged percentage steps against a live Revision |
| 27.04 | Canary analysis gates (`errorRateMaximum`, `p95LatencyMaximum`) | Observe-driven pause + automatic rollback on breach |
| 27.05 | Shadow traffic + manual/automatic promotion | Mirror traffic without serving; explicit or gated advance |
| 27.06 | Deployment freeze + release approval | Block rollouts during a freeze window; require sign-off |
| 27.07 | Regional rollout coordination | Sequence a rollout across `Region`-scoped replica groups |
| 27.08 | Demo `27-canary-rollout` + gate | Breach → pause → rollback; fix → full promotion |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* `strategy` defaults to `rolling` when omitted, so every Deployment spec written against epic 07 keeps working with zero changes.
* Canary steps and gates are declared per-Deployment (`steps: [10, 50, 100]`, `analysis: {...}`) rather than as a global platform default.
* Automatic rollback on gate breach reuses `07.04`'s existing rollback-to-`last_healthy_deployment_id` mechanism; this epic does not invent a second rollback path.
* Weighted routing in Gateway is percentage-of-requests, not percentage-of-replicas — it works even when revision replica counts are uneven during a transition.
* Release approval is a synchronous gate: the reconciler pauses at a named step until an approval API call (or CLI command) unblocks it.

## Open questions

* Does canary analysis poll Observe on an interval, or does Observe push breach events? **Assumption:** poll on the same reconcile interval as `07`, matching epic 07's own resolved approach to readiness polling; push-based alerting arrives with epic 37.
* Is shadow traffic mirrored at the Gateway layer or does it require a second full replica set? **Assumption:** Gateway duplicates requests to shadow replicas and discards responses; shadow replicas run at the same replica count as the canary step, not a separate fixed size.
* Should a deployment freeze block only new rollouts, or also block emergency rollbacks? **Assumption:** freeze blocks new rollouts only; automatic and manual rollbacks always bypass a freeze window.
* How is "region" scoped for regional rollout before epic 39 ships a full `Region` resource? **Assumption:** this epic accepts an opaque `region` label on replica groups now and re-targets the real `Region` kind when epic 39 lands, via an additive field rename documented as a migration note.

## Next step to implement

**27.01 — `Revision` resource + strategy field on Deployment spec** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `27.01-revision-resource-and-strategy-field.md` and assign its `N`).
