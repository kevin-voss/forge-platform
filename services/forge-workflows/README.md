# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Step 16.02: YAML definitions, Ecto/Postgres run + step state, per-run GenServers,
and boot resume so in-flight runs continue without re-executing completed steps.
Step primitives, event triggers, approvals, and compensation land in later steps.
Epic gate: `make demo DEMO=16` (`demos/16-agent-workflow`).

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
RID=$(curl -fsS $P -XPOST $BASE/v1/workflows/fixture-log/runs -H 'content-type: application/json' \
  -d '{"input":{}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])')
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

## Architecture (16.02)

```text
definitions/*.yaml → DefinitionLoader → %Workflow{steps: [...]}
POST /v1/workflows/{name}/runs → persist run(queued) → RunSupervisor starts RunServer
RunServer: for each step → completed? skip : execute → persist(step done) → advance
crash/restart → BootResumer loads non-terminal runs → restart RunServers → resume

ForgeWorkflows.Supervisor (rest_for_one)
├── ForgeWorkflows.Repo
├── Ecto.Migrator
├── ForgeWorkflows.RunRegistry
├── ForgeWorkflows.Engine.RunSupervisor
├── ForgeWorkflows.Engine.BootResumer
└── Bandit → ForgeWorkflowsWeb.Router
```

Idempotency key: `(run_id, step_id)` unique constraint; completed steps are never
re-executed after resume. Runs are project-scoped via `X-Forge-Project`.
