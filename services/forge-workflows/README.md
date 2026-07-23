# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Step 16.04: event triggers + agent steps — durable Events consumer starts
workflows mapped by event type (idempotent via `event_dedup`), and `agent`
steps invoke Forge Agents (`POST /v1/agents/{name}/runs`) with poll-to-completion
(including surfacing `awaiting_approval`). YAML definitions, Ecto/Postgres run +
step state, per-run GenServers, boot resume, and a scheduler for durable
`wake_at` timers. Workflow-level human approval and compensation land in later
steps. Epic gate: `make demo DEMO=16` (`demos/16-agent-workflow`).

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
curl -fsS $P -XPOST $BASE/v1/triggers/test -H 'content-type: application/json' \
  -d '{"event":"deployment.failed","event_id":"evt-smoke-1","data":{"deployment_id":"dep-123","service_id":"svc-1","reason":"unhealthy","failed_at":"2026-07-23T09:00:00Z"}}'
curl -fsS $P $BASE/v1/runs | grep -q dep-123 || true
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
| `FORGE_EVENTS_URL` | `http://forge-events:4105` | Events HTTP API for durable consume |
| `FORGE_WORKFLOWS_EVENTS_ENABLED` | `true` (when URL set) | Set `false` to disable consumer (triggers/test still works) |
| `FORGE_AGENTS_URL` | `http://forge-agents:4301` | Agents HTTP API |
| `FORGE_WORKFLOWS_AGENTS_MODE` | `fake` | `fake\|live\|fail\|awaiting` |
| `FORGE_WORKFLOWS_AGENT_POLL_MS` | `1000` | Agent run poll interval |
| `FORGE_WORKFLOWS_AGENT_STEP_TIMEOUT` | `300000` | Agent step poll deadline (ms) |
| `FORGE_WORKFLOWS_DEFAULT_PROJECT` | `default` | Fallback project for triggers without header |

## Architecture (16.04)

```text
definitions/*.yaml → DefinitionLoader → %Workflow{trigger, steps}
TriggerRegistry: event type → workflow(s)
Events(deployment.failed) → EventConsumer (durable) → event_dedup → start run
POST /v1/triggers/test → same path (synthetic event)
agent step → AgentClient.start_run → poll GET /v1/runs/{id}
           → capture result / surface awaiting_approval into step.output

ForgeWorkflows.Supervisor (rest_for_one)
├── ForgeWorkflows.Repo
├── Ecto.Migrator
├── ForgeWorkflows.RunRegistry
├── ForgeWorkflows.Engine.RunSupervisor
├── ForgeWorkflows.Engine.BootResumer
├── ForgeWorkflows.Engine.Scheduler
├── ForgeWorkflows.Events.Consumer
└── Bandit → ForgeWorkflowsWeb.Router
```

Idempotency: `(run_id, step_id)` for steps; `(event_id, workflow)` in `event_dedup`
for triggers. Duplicate events do not start a second run.

### Definition schema (trigger + agent)

```yaml
name: fixture-trigger
trigger:
  event: deployment.failed
steps:
  - id: diagnose
    type: agent
    agent: deployment-investigator
    input: { deployment: "${event.deployment_id}" }
    retry: { max_attempts: 3, backoff: exponential, base_ms: 200 }
```

Agent input supports `${event.<field>}` templates resolved from the run input
(populated from the triggering event). Agent failures retry per step `retry`
policy from 16.03, then fail the step.
