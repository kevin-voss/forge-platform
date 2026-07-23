# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Step 16.05: durable human `approval` steps — park a run in `awaiting_approval`
with a persisted approval request that survives restart; `approve` resumes,
`deny`/`expire` follow `on_deny` (or fail). Builds on event triggers + agent
steps (16.04), YAML definitions, Ecto/Postgres run/step state, per-run
GenServers, boot resume, and the durable `wake_at` scheduler. Compensation
lands in 16.06. Epic gate: `make demo DEMO=16` (`demos/16-agent-workflow`).

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
curl -fsS $BASE/v1/workflows
curl -fsS $P -XPOST $BASE/v1/workflows/fixture-approval/runs \
  -H 'content-type: application/json' \
  -d '{"input":{"event":{"deployment_id":"dep-1"}}}'
# wait until status awaiting_approval, then:
AID=$(curl -fsS $P $BASE/v1/approvals | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
docker compose restart forge-workflows
curl -fsS $P -H 'X-Forge-Actor: alice' -XPOST $BASE/v1/approvals/$AID/approve
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

## Architecture (16.05)

```text
definitions/*.yaml → DefinitionLoader → %Workflow{trigger, steps}
approval step → ApprovalStore(pending) + run awaiting_approval → RunServer parks
POST /v1/approvals/{id}/approve|deny → decide + wake RunServer
restart while awaiting → BootResumer → park again (no step repeat)
ExpirySweeper → expired → on_deny path (or fail)

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

Idempotency: `(run_id, step_id)` for steps; `(event_id, workflow)` in `event_dedup`
for triggers; `(run_id, step_id)` unique for approvals.

### Definition schema (approval)

```yaml
name: fixture-approval
steps:
  - id: approve-rollback
    type: approval
    prompt: "Approve rollback of ${event.deployment_id}?"
    on_deny: close
  - id: after-approve
    type: log
    message: after-approve
  - id: close
    type: log
    message: closed-denied
```

### Approval API

| Method | Path | Notes |
|---|---|---|
| `GET` | `/v1/approvals` | Project-scoped list |
| `GET` | `/v1/approvals/{id}` | Detail; cross-project → `404` |
| `GET` | `/v1/runs/{id}/approvals` | Approvals for a run |
| `POST` | `/v1/approvals/{id}/approve` | Resume; `X-Forge-Actor` → `decided_by` |
| `POST` | `/v1/approvals/{id}/deny` | Body `{reason}`; follows `on_deny` |
| `GET` | `/v1/runs/{id}` | Includes `pending_approval` when parked |

Terminal decide → `409`. Metrics: `workflow_approvals_total{status}`,
decision latency sum/count.
