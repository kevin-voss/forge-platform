# Epic 41: Usage, quotas, and cost

## Status

Planning

## Milestone

**M3 — Global platform.** Fourth of the six M3 epics (38–43); 43 is the M3 exit capstone.

## Goal

Give the platform a metering, quota, and cost-estimation layer, `forge-usage` (host port `4123`), across CPU seconds, memory seconds, GPU seconds, stored bytes, transferred bytes, database storage, queue operations, model tokens, build minutes, registry storage, and backup storage. When this epic is done, usage is aggregated per organization/project/environment/product, `Quota` and `Budget` resources can cap consumption, optional provider price adapters turn resource usage into an estimated cost, internal chargeback reports can be generated, and the autoscaler can pick a cheaper eligible provider when policy allows it. Proven by `demos/41-usage-and-cost`.

## Why this epic exists

A platform that spans providers and regions (epic 39) and elastic AI capacity (epic 38) needs to answer "how much are we using and what would it cost" before it can be operated responsibly at scale. This epic turns telemetry the platform already emits (epic 12's metrics conventions) into accountable, queryable usage records and gives operators quota/budget guardrails and cost-aware scheduling — without ever requiring a real billing integration to function.

## Relationship to shipped epics

Extends **epic 24 Forge Autoscaler**'s (M1) capacity decisions and **epic 23 Forge Infrastructure**'s (M1) provider selection with a cost dimension, and extends **epic 33 Forge Policy**'s rule set with budget/quota-limit rule types. Surfaces read-only in **epic 40 Forge Console** and consumes metering data instrumented per **epic 12 Forge Observe**'s existing telemetry conventions across every metered service (Runtime, Models, Storage, Queue, Registry, Build, Backup). Compatibility rule: `forge-usage` consumes each service's existing metrics rather than requiring bespoke billing code everywhere — each metered service adds one documented metering event (`resource.<kind>.usage`) alongside its existing telemetry, a superset addition that changes no existing metric or API.

## Primary code areas

* `services/forge-usage/` — new Go service: metering ingestion, aggregation, quota/budget enforcement hook, price adapters
* `demos/41-usage-and-cost/` — usage aggregation + quota + cost-aware placement acceptance

## Suggested language

Go, matching the other `forge-<capability>` platform services.

## Spec references

* `docs/architecture/standalone-cloud.md` § Usage, quotas, and cost
* `specs.md` → Step 12 (Observe) — the metrics conventions metering events extend

## Dependencies

* Epic [`12-forge-observe`](12-forge-observe.md) — source telemetry conventions metering events build on
* Epic `24-forge-autoscaler` (catalogued, not yet materialized) — cost-aware capacity decisions
* Epic `23-forge-infrastructure` (catalogued, not yet materialized) — multi-provider capacity comparison
* Epic `33-forge-policy` (catalogued, not yet materialized) — budget/quota-limit enforcement
* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — resource envelope `UsageRecord`/`Quota`/`Budget` are defined against
* Epic [`40-forge-console`](40-forge-console.md) — optional read-only surfacing, not required for this epic's gate

## Out of scope for this epic

* Real invoicing, payment processing, or any financial transaction — this is internal chargeback and estimation only, never an execution of a payment
* Multi-region cost arbitrage beyond a single-provider price comparison (epic 39 owns region topology itself)
* Plugin-based price adapters beyond the built-in ones (epic 43 adds the extension point)

## Portability contract

`Quota` and `Budget` resources are provider-neutral — expressed in resource units and, optionally, a currency amount — never in a provider-specific billing construct. A provider price adapter is an optional adapter per provider (e.g. Hetzner/AWS/Azure list prices); with none installed, cost estimates simply report resource-unit usage with no dollar figure, so the capability never requires a cloud billing API credential to function on local Docker or bare metal, and behavior is otherwise identical across every target.

```yaml
apiVersion: forge.dev/v1
kind: Budget
metadata:
  name: invoice-platform-monthly
  organization: forge-labs
  project: invoice-platform
spec:
  period: monthly
  limit: { currency: USD, amount: 500 }
  alertThresholds: [0.8, 1.0]
status:
  phase: Ready
  currentSpendEstimate: { currency: USD, amount: 312.40 }
```

## Success demo

```bash
make demo DEMO=41
```

```text
Autoscaler needs 16 more cores for the demo workload
→ Infrastructure compares eligible providers for the target region
→ Policy (epic 33) allows AWS and Hetzner for this project
→ price adapter evaluates both → capacity is created on the
  permitted lower-cost provider
Usage aggregation shows CPU/memory/storage consumption for the demo
project rolling up to the organization level
A Budget nearing its threshold fires an alert (epic 37 integration);
a scale-down recommendation is surfaced for an over-provisioned service
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 41.01 | Usage service skeleton + metering ingestion | Service scaffold, port 4123, metering event intake |
| 41.02 | Usage aggregation by org/project/environment/product | Roll-up queries across every metered dimension |
| 41.03 | `Quota`/`Budget` resources + Policy enforcement hook | Cap consumption; integrate with epic 33 admission |
| 41.04 | Provider price adapters + estimated cost | Optional per-provider list-price adapters |
| 41.05 | Internal chargeback reports | Per-org/project cost breakdown reports |
| 41.06 | Cost-aware scheduling integration | Autoscaler/Infrastructure consult price adapters before provisioning |
| 41.07 | Scale-down recommendations | Flag over-provisioned services from usage history |
| 41.08 | Demo `41-usage-and-cost` + epic gate | Aggregation + quota + cost-aware placement acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* Metering events are emitted by each owning service (Runtime, Models, Storage, Queue, Registry, Build, Backup) as an addition to their existing OTEL/metrics export, not collected by `forge-usage` scraping internals directly.
* Cost estimates are advisory by default; enforcement (blocking a deployment over budget) is a `Policy` (epic 33) rule that consults `forge-usage`, not something `forge-usage` enforces unilaterally.
* Currency amounts are optional on every usage/cost view — resource-unit counts are always available even with zero price adapters installed.
* Chargeback reports are internal accounting artifacts (for the operator's own cost allocation), not customer-facing invoices.

## Open questions

* Aggregation granularity/retention — raw events forever, or rolled up and pruned? Assumption: raw metering events retained briefly (documented window), rolled up into durable per-period aggregates for long-term reporting.
* Does a `Budget` breach block new deployments or only alert? Assumption: alert-only by default (via epic 37 integration); hard blocking is an explicit opt-in `Policy` rule, not the default.
* Token-based model usage (epic 38) — metered per request or per token? Assumption: per token, matching epic 38's token-throughput metrics, aggregated into the same usage model as other dimensions.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **41.01 — Usage service skeleton + metering ingestion** first: aggregation, quotas, and price adapters all need an ingestion path before they have data to work with.
