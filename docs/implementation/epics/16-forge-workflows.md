# Epic 16: Forge Workflows

## Status

In progress

## Goal

Provide durable, multi-step workflow orchestration (Elixir/OTP, `services/forge-workflows`, host port `4302`) that survives service restarts without repeating completed steps, supports retries/delays/timeouts/parallel/conditional step primitives, is triggered by platform events, executes agent steps, persists human approvals across restarts, and can run compensation/rollback actions wired to Control. Proven by `demos/16-agent-workflow`.

## Why this epic exists

AI-native operations need reliable coordination: when a deployment fails, the platform must collect diagnostics, run an agent, request approval, and roll back — reliably, even across crashes. `specs.md` Step 16 requires durable orchestration with compensation. This epic ties Events (11), Agents (15), and Control rollback (07) into repeatable, auditable workflows.

## Primary code areas

* `services/forge-workflows/` — Elixir/OTP application (Phoenix optional for HTTP; or plain Plug/Bandit)
* `services/forge-workflows/lib/workflows/` — engine, definitions, step primitives
* `contracts/openapi/forge-workflows.openapi.yaml`
* `demos/16-agent-workflow/`

## Suggested language

Elixir (per `specs.md` §4 / Step 16). OTP supervision + a durable store (Postgres via Ecto, or embedded) for run state.

## Spec references

* `specs.md` → Step 16: Forge Workflows (features, example workflow, tests, acceptance)
* `specs.md` → Step 11 (Events), Step 15 (Agents), Step 07 (reconciliation/rollback), Step 02 (Control)

## Dependencies

* Epics `00`, `01` conventions
* Epic [`11-forge-events`](11-forge-events.md) for event triggers (minimum: durable subscribe from `11.03`)
* Epic [`15-forge-agents`](15-forge-agents.md) for agent steps (minimum: run + approval from `15.06`)
* Epic [`02-forge-control`](02-forge-control.md) / [`07-deployment-reconciliation`](07-deployment-reconciliation.md) for rollback hooks (minimum: deployment rollback API)

## Out of scope for this epic

* A visual workflow designer / UI
* Distributed multi-node workflow sharding
* Arbitrary user-code execution as steps (steps are typed primitives + platform actions + agent steps)
* Replacing Agents' internal loop (workflows orchestrate agents, not vice versa)

## Success demo

```bash
make demo DEMO=16
```

`demos/16-agent-workflow`: an unhealthy deployment triggers a workflow that collects diagnostics, runs the investigator agent, requests human approval, and — on approval — rolls back the deployment and stores a final report. Restarting the service mid-run resumes without repeating completed steps.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [16.01](../steps/16-forge-workflows/16.01-skeleton-otp-health.md) | Skeleton OTP + health | Complete | Elixir/OTP, health, port 4302 |
| [16.02](../steps/16-forge-workflows/16.02-definitions-durable-state.md) | Definitions + durable run state | Complete | Depends on 16.01; resume across restart |
| [16.03](../steps/16-forge-workflows/16.03-step-primitives.md) | Step primitives | Not started | Depends on 16.02; retry/delay/timeout/parallel/conditional |
| [16.04](../steps/16-forge-workflows/16.04-event-triggers-agent-steps.md) | Event triggers + agent steps | Not started | Depends on 16.03; Events 11, Agents 15 |
| [16.05](../steps/16-forge-workflows/16.05-human-approval-restarts.md) | Human approval across restarts | Not started | Depends on 16.04 |
| [16.06](../steps/16-forge-workflows/16.06-compensation-rollback.md) | Compensation/rollback via Control | Not started | Depends on 16.05; Control/07 rollback |
| [16.07](../steps/16-forge-workflows/16.07-demo-and-gate.md) | Demo `16-agent-workflow` + gate | Not started | Depends on 16.06 |

## Assumptions

* Service at `services/forge-workflows/`, host port `4302`.
* Durable run state persisted in Postgres via Ecto (platform Postgres, separate schema/database) so state survives restarts; embedded alternative documented if Postgres coupling is undesirable.
* Workflow definitions authored as data (YAML/Elixir DSL) describing typed steps — not arbitrary code.
* Determinism in CI achieved via Agents fake mode + fixture events.
* Idempotent step execution keyed by `(run_id, step_id)` so replays never re-run completed steps.

## Open questions

* Definition format: declarative YAML vs an Elixir DSL module. Assumption: YAML definitions loaded into an internal struct for portability; DSL optional later.
* State store: platform Postgres (Ecto) vs embedded (Mnesia/DETS). Assumption: Postgres via Ecto for durability + queryable history.
* Approval transport: shared with Agents' approval store vs Workflows-owned approvals. Assumption: Workflows owns workflow-level approvals but can consume Agents' approval when a step is an agent run.
* Compensation model: per-step compensators vs saga log. Assumption: per-step compensators recorded in a saga log, executed in reverse on rollback.

## Next step to implement

**[16.03](../steps/16-forge-workflows/16.03-step-primitives.md) — Step primitives** (retry/delay/timeout/parallel/conditional).
