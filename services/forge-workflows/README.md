# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Step 16.06: saga-style compensation — forward steps may declare `compensate`
actions recorded in `workflow_saga_log`; on step failure or an explicit
`rollback` action (typically after human approval), compensators run in reverse
order (idempotent), including `control.rollback_deployment` via a Control client
(fake or live PATCH to last healthy image). Final reports store `rolled_back`
+ `report_ref` on the run. Builds on durable approvals (16.05), event triggers +
agent steps, YAML definitions, and the durable scheduler. Epic gate
(`make demo DEMO=16` / `demos/16-agent-workflow`) is complete.

## Local

```bash
# from repo root
make service-run SERVICE=forge-workflows
make service-test SERVICE=forge-workflows

# or inside this directory
make compile
make run
make test
```

### Smoke

```bash
curl -fsS localhost:4302/health/live
curl -fsS localhost:4302/health/ready
curl -fsS localhost:4302/ | grep -q '"service":"forge-workflows"'

BASE=localhost:4302; P='-H X-Forge-Project:proj-a'
curl -fsS $P -XPOST $BASE/v1/workflows/fixture-compensation-approve/runs \
  -H 'content-type: application/json' \
  -d '{"input":{"event":{"deployment_id":"dep-1"}}}'
# wait until awaiting_approval, approve, then:
curl -fsS $P $BASE/v1/runs | grep -o '"rolled_back":true' | head -1
```

OpenAPI (canonical): [`contracts/openapi/forge-workflows.openapi.yaml`](../../contracts/openapi/forge-workflows.openapi.yaml).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (container) | Required; Compose maps host `4302` → `8080` |
| `FORGE_SERVICE_NAME` | `forge-workflows` | Identity `service` field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity `version` field |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | Logged at startup |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Bandit / OTP shutdown timeout |
| `FORGE_WORKFLOWS_DATABASE_URL` | (required) | Postgres URL; Compose uses `forge_workflows` DB |
| `FORGE_WORKFLOWS_DEFS_DIR` | `definitions/` | YAML workflow definitions directory |
| `FORGE_WORKFLOWS_POOL_SIZE` | `5` | Ecto pool size |
| `FORGE_WORKFLOWS_MAX_PARALLELISM` | `8` | Cap on parallel branch concurrency |
| `FORGE_WORKFLOWS_DEFAULT_STEP_TIMEOUT` | `300000` | Default step timeout (ms) |
| `FORGE_WORKFLOWS_SCHEDULER_TICK_MS` | `1000` | Durable timer poll interval |
| `FORGE_WORKFLOWS_APPROVAL_TTL_SECONDS` | `86400` | Pending approval TTL before auto-expire |
| `FORGE_EVENTS_URL` | `http://forge-events:4105` | Events HTTP API for durable consume |
| `FORGE_WORKFLOWS_EVENTS_ENABLED` | `true` (when URL set) | Set `false` to disable consumer (triggers/test still works) |
| `FORGE_AGENTS_URL` | `http://forge-agents:4301` | Agents HTTP API |
| `FORGE_WORKFLOWS_AGENTS_MODE` | `fake` | `fake\|live\|fail\|awaiting` |
| `FORGE_WORKFLOWS_AGENT_POLL_MS` | `1000` | Agent run poll interval |
| `FORGE_WORKFLOWS_AGENT_STEP_TIMEOUT` | `300000` | Agent step poll deadline (ms) |
| `FORGE_WORKFLOWS_DEFAULT_PROJECT` | `default` | Fallback project for triggers without header |
| `FORGE_CONTROL_URL` | `http://forge-control:4001` | Control API for apply/rollback |
| `FORGE_WORKFLOWS_CONTROL_MODE` | `fake` | `fake\|live\|fail` |
| `FORGE_WORKFLOWS_CONTROL_HTTP_TIMEOUT_MS` | `10000` | Control HTTP timeout |
| `FORGE_WORKFLOWS_REPORT_BUCKET` | (empty) | Optional Storage bucket for report refs |

## Architecture (16.06)

```text
forward step done + compensate: → saga_log(pending)
failure OR action:rollback → status=compensating → reverse compensators (idempotent)
control.rollback_deployment → ControlClient (GET reconcile lastHealthyImage + PATCH)
report.store → run.result {rolled_back, report_ref}

ForgeWorkflows.Supervisor (rest_for_one)
├── ForgeWorkflows.Repo
├── Ecto.Migrator
├── ForgeWorkflows.RunRegistry
├── ForgeWorkflows.Engine.RunSupervisor
├── ForgeWorkflows.Engine.BootResumer
├── ForgeWorkflows.Engine.Scheduler
├── ForgeWorkflows.Approvals.ExpirySweeper
├── ForgeWorkflows.Events.Consumer
└── Bandit → ForgeWorkflowsWeb.Router (+ ApprovalsController)
```

Idempotency: `(run_id, step_id)` for steps and saga log entries; compensators
claim `pending|running` → `compensated` so crash mid-rollback resumes without
double-acting completed entries.

### Definition schema (compensation)

```yaml
name: fixture-compensation-approve
steps:
  - id: apply-change
    type: task
    action: control.apply
    compensate: control.rollback_deployment
  - id: approve-rollback
    type: approval
    prompt: "Approve rollback of ${event.deployment_id}?"
    on_deny: close
  - id: do-rollback
    type: task
    action: rollback
  - id: finalize
    type: task
    action: report.store
  - id: close
    type: log
    message: closed-denied
```

### Control client

| Mode | Behavior |
|---|---|
| `fake` | In-process ETS; records apply/rollback calls (CI default) |
| `live` | `GET /v1/deployments/{id}/reconcile` → `PATCH` with `lastHealthyImage` |
| `fail` | All Control calls error |

Metrics: `workflow_compensations_total{status}`, `workflow_rollbacks_total{result}`.
