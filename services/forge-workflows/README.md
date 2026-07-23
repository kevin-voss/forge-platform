# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Step 16.03: step primitives — retry (persisted attempts + backoff), durable delay
timers, step/run timeouts, parallel fan-out/fan-in, and safe conditional branching.
YAML definitions, Ecto/Postgres run + step state, per-run GenServers, boot resume,
and a scheduler that fires due `wake_at` timers. Event triggers, approvals, and
compensation land in later steps. Epic gate: `make demo DEMO=16`
(`demos/16-agent-workflow`).

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
RID=$(curl -fsS $P -XPOST $BASE/v1/workflows/fixture-primitives/runs -H 'content-type: application/json' \
  -d '{"input":{"severity":"high"}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])')
curl -fsS $P $BASE/v1/runs/$RID | grep -q '"steps"'
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

## Architecture (16.03)

```text
definitions/*.yaml → DefinitionLoader → %Workflow{steps: [...]}
POST /v1/workflows/{name}/runs → persist run(queued) → RunSupervisor starts RunServer
RunServer: dispatch by step type
  task/log/noop/timeout → execute (+ optional retry with wake_at backoff)
  delay → persist wake_at (waiting); Scheduler / local timer resumes once
  parallel → fan-out child steps (parent_step_id), join (collect-then-fail)
  conditional → safe predicate over context → then/else (other skipped)
crash/restart → BootResumer + Scheduler boot-scan due wake_at → resume

ForgeWorkflows.Supervisor (rest_for_one)
├── ForgeWorkflows.Repo
├── Ecto.Migrator
├── ForgeWorkflows.RunRegistry
├── ForgeWorkflows.Engine.RunSupervisor
├── ForgeWorkflows.Engine.BootResumer
├── ForgeWorkflows.Engine.Scheduler
└── Bandit → ForgeWorkflowsWeb.Router
```

Idempotency key: `(run_id, step_id)` unique constraint; completed/skipped steps are
never re-executed after resume. Delays and retry backoffs use durable `wake_at`.
Parallel children are step rows with `parent_step_id`. Runs are project-scoped via
`X-Forge-Project`.

### Definition schema (primitives)

```yaml
steps:
  - id: collect
    type: task
    action: noop
    retry: { max_attempts: 3, backoff: exponential, base_ms: 200 }
  - id: wait
    type: delay
    delay_ms: 5000
  - id: fanout
    type: parallel
    branches: [{id: logs, type: noop}, {id: metrics, type: noop}]
  - id: decide
    type: conditional
    when: "context.severity == 'high'"
    then: escalate
    else: close
```

Conditional predicates are a closed safe expression language (`context.<key>`,
`==` / `!=` with string literals) — no arbitrary code execution.
