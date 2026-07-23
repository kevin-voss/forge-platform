# Epic 15: Forge Agents

## Status

In progress

## Goal

Provide a safe, permission-aware agent execution runtime (Python, `services/forge-agents`, host port `4301`) where agents defined in YAML select a model, call only registered tools subject to per-call permission checks, run within enforced step/time limits with an auditable history, and require explicit human approval before any destructive action. When done, seed agents (deployment-investigator, log-summarizer, docs-assistant, release-reviewer, infra-health) run via API and `forge agent` CLI, proven by `demos/15-agent-runtime`.

## Why this epic exists

The platform's AI-native value comes from agents that can inspect and act on the system safely. Without a permission-aware runtime, agents could call arbitrary tools or take destructive actions. This epic establishes the guardrails (registered tools, permission checks, limits, approval, audit) that Workflows (16) and the capstone (19) depend on.

## Primary code areas

* `services/forge-agents/` — Python service (FastAPI)
* `services/forge-agents/tools/` — platform tool adapters (Control/Runtime/Observe/Storage/Models/Events)
* `services/forge-agents/agents/` — YAML agent definitions
* `contracts/openapi/forge-agents.openapi.yaml`
* `demos/15-agent-runtime/`
* `tools/forge-cli` — `forge agent` subcommands

## Suggested language

Python (per `specs.md` §4 / Step 15). FastAPI + async tool calls.

## Spec references

* `specs.md` → Step 15: Forge Agents (definition, features, initial agents, tests, acceptance)
* `specs.md` → Step 14 (Models), Step 12 (Observe), Step 04 (Runtime), Step 02 (Control), Step 11 (Events), Step 13 (Storage)

## Dependencies

* Epics `00`, `01` conventions
* Epic [`14-forge-models`](14-forge-models.md) for inference (minimum: generate/classify from `14.04`, deterministic fake backend for CI)
* Epic [`09-forge-identity`](09-forge-identity.md) for permissions/project scope
* Epics [`02-forge-control`](02-forge-control.md), [`04-forge-runtime`](04-forge-runtime.md), [`12-forge-observe`](12-forge-observe.md), [`13-forge-storage`](13-forge-storage.md), [`11-forge-events`](11-forge-events.md) for platform tools (tools degrade gracefully / use fakes when a backing service is unavailable in CI)

## Out of scope for this epic

* Durable multi-step orchestration across services (that is Workflows, 16)
* Memory/retrieval tool (that is epic 17; Agents exposes the seam, wiring lands in 17.05)
* Training/fine-tuning
* Autonomous destructive actions without approval (explicitly forbidden)

## Success demo

```bash
make demo DEMO=15
```

`demos/15-agent-runtime`: a deliberately failing deployment is created; the deployment-investigator agent inspects status, reads logs, inspects the readiness failure, produces a diagnosis, and recommends an action — without restarting anything unless a human approves.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [15.01](../steps/15-forge-agents/15.01-skeleton.md) | Skeleton | Complete | Python/FastAPI, health, port 4301 |
| [15.02](../steps/15-forge-agents/15.02-agent-registry-yaml.md) | Agent registry + YAML definitions | Not started | Depends on 15.01 |
| [15.03](../steps/15-forge-agents/15.03-tool-registry-permissions.md) | Tool registry + per-call permission checks | Not started | Depends on 15.02 |
| [15.04](../steps/15-forge-agents/15.04-run-engine.md) | Run engine: max steps, timeouts, history | Not started | Depends on 15.03; Models 14.04 |
| [15.05](../steps/15-forge-agents/15.05-platform-tools.md) | Platform tools | Not started | Depends on 15.04; Control/Runtime/Observe/Storage/Models/Events |
| [15.06](../steps/15-forge-agents/15.06-human-approval.md) | Human approval for destructive tools | Not started | Depends on 15.05 |
| [15.07](../steps/15-forge-agents/15.07-seed-agents-cli.md) | Seed agents + CLI `forge agent` | Not started | Depends on 15.06 |
| [15.08](../steps/15-forge-agents/15.08-demo-and-gate.md) | Demo `15-agent-runtime` + gate | Not started | Depends on 15.07 |

## Assumptions

* Service at `services/forge-agents/`, host port `4301`.
* CI uses a deterministic fake model (`forge-models` fake backend) so agent runs are reproducible; a "fake tool" mode lets tool tests run without live backing services.
* Permissions are declared per agent (`permissions:`) and checked before every tool call against the run's identity/project scope.
* Destructive tools are explicitly tagged (`destructive: true`) and always require approval regardless of permissions.
* Run history + audit persisted (SQLite under a service volume, mirroring the storage epic's self-contained approach).

## Open questions

* Agent reasoning loop: implement a minimal ReAct-style loop with the fake model, or a scripted planner for CI determinism? Assumption: deterministic scripted loop driven by the fake model's stable outputs for CI; real model optional.
* Approval delivery: synchronous poll vs event/webhook? Assumption: approval request persisted + polled via API in this epic; Events/Workflows integration deepens in 16.
* Tool backing in CI: real services vs fakes? Assumption: fakes/stubs by default; integration flag to hit live services locally.
* History persistence store: SQLite vs platform Postgres. Assumption: SQLite for service self-containment.

## Next step to implement

**[15.02](../steps/15-forge-agents/15.02-agent-registry-yaml.md) — Agent registry + YAML definitions**.
