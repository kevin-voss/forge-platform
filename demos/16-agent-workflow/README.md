# Demo 16: Agent workflow

End-to-end acceptance gate for epic 16 (Forge Workflows). A standalone Compose
stack brings up durable `forge-workflows` (Postgres), `forge-models`
(`FORGE_MODELS_BACKEND=fake`), and `forge-agents` (`FORGE_AGENTS_TOOLS_MODE=fake`).
A fixture `deployment.failed` event starts the `incident-response` workflow,
which collects diagnostics in parallel, runs the investigator agent, parks on
human approval, survives a workflows restart without repeating completed steps,
then — on approval — rolls back via the fake Control client and stores a final
report with `rolled_back=true`.

```text
1. deployment.failed (fixture /v1/triggers/test) → workflow starts
2. collect logs + metrics (parallel)
3. run investigator agent → analysis
4. request human approval → run awaiting_approval
5. restart forge-workflows → run resumes, no completed step repeated
6. approve → rollback deployment (fake Control) → store final report
7. run ends completed with rolled_back=true
```

```text
acceptance.sh (host)
        │  HTTP
        ▼
forge-workflows :4302 ──fake agent──► in-process Fake agent result
        │                ──fake Control──► ControlClient.Fake (rollback)
        │                ──Postgres──────► durable runs/steps/approvals/saga
        │
forge-agents :4301 (fake tools; wired for the stack)
forge-models :4300 (fake; wired for the stack)
```

## What this demo checks

* OpenAPI contract for workflows documents the used paths.
* `incident-response` is registered with `trigger.event=deployment.failed`.
* Fixture event injection starts exactly one durable run.
* Parallel diagnostic branches complete before the agent step.
* Agent step produces a diagnosis for the failing deployment.
* Run enters `awaiting_approval` with a durable pending approval.
* Restarting `forge-workflows` preserves approval state and does not re-run
  completed steps (`attempt` stays `1`).
* Approval triggers saga compensation (`control.rollback_deployment`) and
  `report.store`; run result has `rolled_back=true` + `report_ref`.
* Run history is fully auditable.
* Deterministic CI path: fake agents client + fake Control (no live Control HTTP).

## Run

From the repository root:

```bash
make demo DEMO=16
```

Expect a final `demo 16 PASSED` line and exit code `0`. On failure the script
dumps workflows/agents/models logs plus recent runs, then tears down with
`docker compose down -v`.

Optional bring-up only (leaves the stack running):

```bash
./demos/16-agent-workflow/run.sh --phase=up
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_WORKFLOWS_URL` | `http://127.0.0.1:4302` | Host workflows API + readiness |
| `FORGE_AGENTS_URL` | `http://127.0.0.1:4301` | Host agents API + readiness |
| `FORGE_MODELS_URL` | `http://127.0.0.1:4300` | Host models API + readiness |
| `FORGE_WORKFLOWS_AGENTS_MODE` | `fake` | Deterministic agent step results |
| `FORGE_WORKFLOWS_CONTROL_MODE` | `fake` | In-process Control apply/rollback |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | Agents fake tools (stack wiring) |
| `FORGE_MODELS_BACKEND` | `fake` | Models fake backend |
| `FORGE_WORKFLOWS_PROJECT` | `demo-16` | `X-Forge-Project` scope |
| `FORGE_WORKFLOWS_DEPLOYMENT` | `dep-failing` | Fixture deployment id in the event |

## Workflow definition

`definitions/incident-response.yaml` matches the Step 16 example flow:
event → parallel collect → agent → approval → rollback → report.

Event injection uses `POST /v1/triggers/test` (documented Events fixture path)
so the gate stays deterministic without requiring a live Events/NATS stack.

## Security notes

* No credentials or secrets; agents/models/Control are fully local/fake.
* Rollback runs only after an explicit approve call in `acceptance.sh`.
* Suitable for CI regression of durability + approval + compensation.
