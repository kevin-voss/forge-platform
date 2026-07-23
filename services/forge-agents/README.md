# forge-agents

Python/FastAPI agent runtime service (epic 15). Host port **4301**.

Skeleton (15.01), agent registry (15.02), tool registry with per-call
permission checks (15.03), and the bounded run engine with audit history
(15.04). YAML agents load at startup; fake tools
(`echo.ping`, `fail.raise`, `deployment.read`) register under
`FORGE_AGENTS_TOOLS_MODE=fake` (CI default). Platform tools, approval, CLI,
and the epic gate (`make demo DEMO=15`) arrive in later steps.

## Local

```bash
# from repo root
make service-run SERVICE=forge-agents
make service-test SERVICE=forge-agents

# or inside this directory
make sync
make dev          # http://127.0.0.1:4301
make test-unit
make lint
```

### Smoke

```bash
curl -fsS localhost:4301/health/live
curl -fsS localhost:4301/health/ready
curl -fsS localhost:4301/
curl -fsS localhost:4301/v1/agents | grep -q fixture-echo
curl -s -o /dev/null -w '%{http_code}\n' localhost:4301/v1/agents/nope   # 404
curl -fsS localhost:4301/v1/tools | grep -q '"required_permissions"'

# Dry-run (deterministic fake model + fake tools)
RID=$(curl -fsS -XPOST localhost:4301/v1/agents/fixture-echo/runs \
  -H 'content-type: application/json' -H 'X-Forge-Project: proj-a' \
  -d '{"input":"hello","context":{"dry_run":true}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])')
sleep 1
curl -fsS localhost:4301/v1/runs/$RID -H 'X-Forge-Project: proj-a' | grep -q '"steps"'
```

OpenAPI (canonical): [`contracts/openapi/forge-agents.openapi.yaml`](../../contracts/openapi/forge-agents.openapi.yaml).

## Agent registry

Definitions are YAML files in `FORGE_AGENTS_DEFS_DIR` (default packaged
`agents/`). Each file is one agent:

```yaml
name: fixture-echo
model: local-general
tools: [echo.ping]
permissions: [project:read]
limits:
  max_steps: 3
  timeout_seconds: 30
```

Validation rejects unknown fields, malformed tool/permission ids, duplicate
agent names, and out-of-bounds limits (`max_steps` 1–100, `timeout_seconds`
1–3600). Malformed or duplicate definitions fail process startup with the file
path and reason.

## Tool registry + permissions

`GET /v1/tools` lists registered tools with JSON Schema input/output,
`destructive`, and `required_permissions`. The internal `ToolInvoker`
enforces, deny-by-default:

1. tool exists in the registry → else `unknown_tool` (hallucination)
2. tool is declared on the agent → else `not_declared` (overreach)
3. arguments match `input_schema` → else `invalid_arguments`
4. call scope has every required permission → else `permission_denied`
5. `tool.execute(args)`

Every decision is audited in structured logs (`decision`, `reason`) and counted
on in-process metrics `agent_tool_calls_total` / `agent_tool_denied_total`.

Fake tools (mode `fake`):

| Name | Permissions | Notes |
|---|---|---|
| `echo.ping` | `project:read` | Echoes `message` |
| `fail.raise` | `project:read` | Raises at execute time |
| `deployment.read` | `deployment:read` | Stub deployment payload |

## Run engine

`POST /v1/agents/{name}/runs` starts a bounded loop (model decide → optional
tool → observe → repeat) under the agent's `max_steps` and `timeout_seconds`.
Runs are project-scoped via `X-Forge-Project`.

| Endpoint | Notes |
|---|---|
| `POST /v1/agents/{name}/runs` | `202 {run_id,status:running}` |
| `GET /v1/runs/{id}` | Status + ordered audit `steps` |
| `GET /v1/runs` | Project-scoped list |
| `POST /v1/runs/{id}/cancel` | `200 cancelled` or `409` if terminal |

Hard ceilings:

* exhaust `max_steps` without a final answer → `stopped` / `max_steps_exceeded`
* wall-clock timeout → `failed` / `timeout`
* cancel → `cancelled`; cancel of a terminal run → `409`

Pass `"context":{"dry_run":true}` to use the deterministic fake model planner
(no forge-models required). Live runs call `FORGE_MODELS_URL` via
`HttpModelClient`. Every model/tool/final turn is persisted in SQLite
(`FORGE_AGENTS_DB_PATH`) for audit. Metrics: `agent_runs_total{status}`,
`agent_run_steps`, run duration histogram (in-process).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4301` |
| `FORGE_MODELS_URL` | `http://forge-models:4300` | Models base URL; must be absolute http(s) |
| `FORGE_AGENTS_DEFS_DIR` | packaged `agents/` | Directory of `*.yaml` / `*.yml` agent definitions |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | `fake\|live` — live adapters arrive in 15.05 |
| `FORGE_AGENTS_DB_PATH` | `/data/agents/runs.db` | SQLite run + step audit store |
| `FORGE_AGENTS_MAX_CONCURRENT_RUNS` | `4` | In-flight run cap (`429` when exceeded) |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-agents` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-agents/
├── agents/                 # YAML agent definitions (fixture-echo for tests)
├── migrations/             # SQLite schema for runs + run_steps
├── app/
│   ├── main.py             # FastAPI factory + lifespan
│   ├── config.py           # pydantic-settings
│   ├── health.py           # /health/live, /health/ready
│   ├── logging.py          # JSON logs + X-Request-ID middleware
│   ├── permissions.py      # CallScope + PermissionChecker
│   ├── agents/             # models + YAML loader + registry
│   ├── tools/              # Tool base, registry, fake tools, invoker
│   ├── run/                # RunEngine, RunStore, ModelClient
│   └── api/                # agents, tools, runs routes
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```
