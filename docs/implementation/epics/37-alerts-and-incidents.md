# Epic 37: Alerts and incidents

## Status

Planning

## Milestone

**M2 — Production platform.** This epic is the **M2 exit gate** — when its demo passes, the platform has proven production deployment strategies, durable queues, HA database, volumes, distributed storage, HA secrets, policy, TLS/DNS, an HA control plane, backups, and now closed-loop incident response.

## Goal

Turn epic 12's basic service-down/error-rate alerts into a full alerting and incident-management capability that closes the operations loop end to end without a human writing a diagnosis by hand. When this epic is done, metric, log, trace-derived, and synthetic-check alerts fire through dedup/grouping/silence/maintenance-window logic into escalation policies; every firing alert creates or attaches to an `Incident` record with deployment metadata; an incident automatically triggers an Agents investigation and, when the investigator identifies a likely-bad revision, a Workflows run that requests human approval before rolling back. Proven by `demos/37-incident-response`, and marked as the **M2 exit gate**.

## Why this epic exists

Epic 19's capstone demonstrated the detect → diagnose → approve → rollback loop as a one-off scripted scenario. A production platform needs that loop to be a standing, reusable capability triggered by real alert conditions, not a demo script calling Workflows directly. This epic generalizes epic 12's alert rules into a first-class alerting/incident service and wires it permanently into epics 15 and 16, so *any* future alert condition — not just the capstone's specific failure — can start the same investigate-approve-rollback pipeline.

## Relationship to shipped epics

Extends **epic 12 Forge Observe** (basic service-down/error-rate alert rules, already shipped) into a dedicated `forge-alerts` service that adds dedup, grouping, silence/maintenance windows, and escalation on top; wires directly into **epic 15 Forge Agents** (investigation trigger) and **epic 16 Forge Workflows** (rollback-approval trigger) — formalizing, as an always-on capability, the same investigate → approve → rollback pattern epic 19's capstone proved manually. Compatibility rule: `forge-alerts` (port 4118) subscribes to Observe's existing metrics/logs/traces query APIs (a facade) rather than replacing Observe's alerting; epic 12's original alert rules keep firing and are simply one input among several that now feed `Incident` records.

## Primary code areas

* `services/forge-alerts/` — new Go service: alert rule evaluation, dedup/grouping, `Incident` lifecycle, escalation
* `services/forge-agents/` — investigation-trigger tool consumed by an incident (extends epic 15's tool registry)
* `services/forge-workflows/` — rollback-approval workflow triggered by an incident (extends epic 16's event triggers)
* `demos/37-incident-response/` — induced error rate → incident → agent → approved rollback

## Suggested language

Go, matching Observe (epic 12) and the other `forge-<capability>` operational services.

## Spec references

* `docs/architecture/standalone-cloud.md` § Alerts and incidents
* `specs.md` → Step 12 (Observe basic alerts baseline), Step 15 (Agents investigation), Step 16 (Workflows rollback)

## Dependencies

* Epic [`12-forge-observe`](12-forge-observe.md) — metrics/logs/traces query APIs and the basic alert-rule baseline being extended
* Epic [`15-forge-agents`](15-forge-agents.md) — investigation agent, human-approval-gated destructive tools
* Epic [`16-forge-workflows`](16-forge-workflows.md) — durable workflow, event triggers, compensation/rollback
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — rollback API the workflow calls
* Epic `27-deployment-strategies` (catalogued, not yet materialized) — revision metadata attached to an incident
* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — resource envelope `AlertRule`/`Incident` are defined against

## Out of scope for this epic

* Cost- or capacity-based alerts (epic 41)
* Multi-region failover alerting (epic 39)
* Plugin-based third-party notification providers (epic 43 adds the extension point; this epic ships built-in channels only)
* Changing epic 15/16's internal agent or workflow engine — only new trigger wiring is added

## Portability contract

An `AlertRule` or `Incident` references Forge-native metric/log/trace queries only — no product-manifest or platform-resource field ever names a cloud monitoring provider (no CloudWatch, no Azure Monitor). Behavior is identical on Docker, bare metal, Hetzner, AWS, and Azure because Observe's backends (Prometheus/Tempo/Loki, from foundation epic 00) are always the Forge-owned stack regardless of target. Escalation notification channels are pluggable but default to a built-in log/webhook sink, so the demo gate never requires an external pager or SaaS credential.

```yaml
apiVersion: forge.dev/v1
kind: AlertRule
metadata:
  name: gateway-error-rate
  project: invoice-platform
  environment: production
spec:
  source: metric
  query: rate(gateway_requests_total{status=~"5.."}[5m]) > 0.05
  for: 2m
  severity: critical
  groupBy: [deployment]
status:
  phase: Ready
  firing: false
```

## Success demo

```bash
make demo DEMO=37
```

```text
Gateway error rate exceeds 5% on the demo deployment
→ alert fires → Incident created, deployment metadata attached
→ investigation agent (epic 15) runs automatically, inspects logs/metrics/
  traces, identifies the recently rolled-out revision as the cause
→ workflow (epic 16) requests human approval to roll back
→ operator approves via API/CLI
→ Forge Deploy rolls back to the last healthy revision (epic 07/27)
→ error rate recovers; Incident marked resolved with a final report
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 37.01 | Alerts service skeleton + `AlertRule`/`Incident` resources | Service scaffold, resource model, port 4118 |
| 37.02 | Metric/log/trace alert evaluation + synthetic checks | Evaluate rules against Observe's query APIs |
| 37.03 | Dedup, grouping, silence, and maintenance windows | Prevent alert storms; planned-maintenance suppression |
| 37.04 | Escalation policies + notification channels | Tiered escalation; built-in log/webhook sink |
| 37.05 | Incident → agent investigation trigger | Firing incident automatically starts an Agents run |
| 37.06 | Incident → workflow rollback trigger + human approval | Investigation result starts a Workflows rollback-approval run |
| 37.07 | Demo `37-incident-response` + M2 exit gate | Full detect → diagnose → approve → rollback acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* One `Incident` can aggregate multiple firing `AlertRule`s (grouping by deployment/service) rather than creating one incident per rule per firing.
* The investigation trigger reuses epic 15's existing tool-permission model; no new bypass of the destructive-action approval gate is introduced.
* Escalation policies are declarative (ordered channel list + wait intervals), not arbitrary code.
* CI determinism reuses epics 15/16's fake-model/fixture-event modes so the demo's alert → incident → agent → workflow chain is reproducible without a live model.

## Open questions

* Does an `Incident` own its own approval, or does it delegate entirely to the epic 16 workflow's approval? Assumption: the workflow owns the approval (per epic 16's design); `Incident` only records the outcome for audit/history.
* How long does a resolved `Incident` remain queryable? Assumption: same retention as other audit records (platform-wide retention policy, not epic-specific); no separate incident-only retention rule in this epic.
* Should synthetic checks run from Gateway's edge or from Observe? Assumption: Observe schedules and records synthetic checks; Gateway is one of the endpoints they probe, not the runner.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **37.01 — Alerts service skeleton + `AlertRule`/`Incident` resources** first: every later step (evaluation, escalation, agent/workflow triggers) needs the resource model and service scaffold in place.
