# forge-agents

Python/FastAPI agent runtime service (epic 15). Host port **4301**.

Skeleton (15.01), agent registry (15.02), tool registry with per-call
permission checks (15.03), bounded run engine with audit history (15.04),
platform tools (15.05), human approval for destructive tools (15.06), and
seed agents plus `forge agent` CLI (15.07). Fake tool mode
(`FORGE_AGENTS_TOOLS_MODE=fake`) is the CI default. The epic gate
(`make demo DEMO=15`) arrives in 15.08.

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
curl -fsS localhost:4301/v1/tools | python3 -c 'import sys,json;\
print(any(t["name"]=="runtime.restart" and t["destructive"] for t in json.load(sys.stdin)["tools"]))'

# Dry-run (deterministic fake model + fake tools)
RID=$(curl -fsS -XPOST localhost:4301/v1/agents/fixture-echo/runs \
  -H 'content-type: application/json' -H 'X-Forge-Project: proj-a' \
  -d '{"input":"hello","context":{"dry_run":true}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])')
sleep 1
curl -fsS localhost:4301/v1/runs/$RID -H 'X-Forge-Project: proj-a' | grep -q '"steps"'

# Approvals (when a run is awaiting_approval for a destructive tool)
curl -fsS localhost:4301/v1/approvals -H 'X-Forge-Project: proj-a'
# AID=...; curl -fsS -XPOST localhost:4301/v1/approvals/$AID/deny \
#   -H 'content-type: application/json' -H 'X-Forge-Project: proj-a' \
#   -H 'X-Forge-Actor: alice' -d '{"reason":"manual"}'
```

OpenAPI (canonical): [`contracts/openapi/forge-agents.openapi.yaml`](../../contracts/openapi/forge-agents.openapi.yaml).

## Seed agents (15.07)

Five least-privilege seed definitions ship beside `fixture-echo`:

| Agent | Tools | Destructive |
|---|---|---|
| `deployment-investigator` | `deployment.read`, `logs.search`, `metrics.query`, `runtime.restart` | `runtime.restart` (approval-gated) |
| `log-summarizer` | `logs.search`, `models.generate` | — |
| `docs-assistant` | `storage.get`, `models.generate` | — |
| `release-reviewer` | `deployment.read`, `logs.search`, `models.generate` | — |
| `infra-health` | `metrics.query`, `deployment.read`, `logs.search` | — |

Docs: [`docs/agents/seed-agents.md`](../../docs/agents/seed-agents.md).

CLI (`FORGE_AGENTS_URL`, default `http://127.0.0.1:4301`):

```bash
forge agent list
forge agent run log-summarizer --project proj-a --input "errors x3" --dry-run --json
forge agent run deployment-investigator --project proj-a --deployment dep-1 \
  --tool runtime.restart --dry-run
forge agent deny <approval-id> --project proj-a --reason "not yet"
forge agent status <run-id> --project proj-a
```

`forge agent run` defaults to `--wait` (poll until terminal or `awaiting_approval`).
Awaiting approval exits non-zero with a clear message; use `approve` / `deny` next.

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
5. `tool.execute(args)` — backend failures normalize to
   `{tool, error_code, message}` (`backend_unavailable`, `tool_timeout`, …)
   and never crash the run

Every decision is audited in structured logs (`decision`, `reason`) and counted
on in-process metrics `agent_tool_calls_total` / `agent_tool_denied_total`.
Live backends also record `agent_tool_backend_latency_seconds` and
`agent_tool_backend_errors_total`.

### CI helper tools

| Name | Permissions | Notes |
|---|---|---|
| `echo.ping` | `project:read` | Echoes `message` |
| `fail.raise` | `project:read` | Normalized execute failure |

### Platform tools (15.05)

| Name | Permissions | Destructive | Backend |
|---|---|---|---|
| `deployment.read` | `deployment:read` | no | Control |
| `logs.search` | `logs:read` | no | Observe |
| `metrics.query` | `metrics:read` | no | Observe |
| `runtime.restart` | `runtime:restart` | **yes** | Runtime |
| `storage.get` | `storage:read` | no | Storage |
| `storage.put` | `storage:write` | no | Storage |
| `models.generate` | `models:generate` | no | Models |
| `models.embed` | `models:embed` | no | Models |
| `events.publish` | `events:publish` | no | Events |

`FORGE_AGENTS_TOOLS_MODE=fake` returns deterministic fixtures under
`app/tools/fixtures/`. `live` uses httpx clients against the service URLs
below. Destructive tools never execute without a persisted approval.

## Run engine

`POST /v1/agents/{name}/runs` starts a bounded loop (model decide → optional
tool → observe → repeat) under the agent's `max_steps` and `timeout_seconds`.
Runs are project-scoped via `X-Forge-Project`.

| Endpoint | Notes |
|---|---|
| `POST /v1/agents/{name}/runs` | `202 {run_id,status:running}` |
| `GET /v1/runs/{id}` | Status + ordered audit `steps` (+ `pending_approval` when paused) |
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

## Human approval (15.06)

When the model requests a `destructive: true` tool (e.g. `runtime.restart`):

1. permission/schema checks run as usual
2. an approval request is persisted; run status → `awaiting_approval`
3. the tool executes **only** after `POST /v1/approvals/{id}/approve`
4. `deny` / TTL expiry skips the tool (run continues without executing it)

| Endpoint | Notes |
|---|---|
| `GET /v1/approvals` | Project-scoped list (`?status=pending`) |
| `GET /v1/approvals/{id}` | Detail; cross-project → `404` |
| `POST /v1/approvals/{id}/approve` | `200 {status:approved}`; resumes run |
| `POST /v1/approvals/{id}/deny` | body `{reason}`; `200 {status:denied}` |
| terminal decide again | `409` |

`X-Forge-Actor` is recorded as `decided_by` (defaults to `anonymous`).
Pending approvals expire after `FORGE_AGENTS_APPROVAL_TTL_SECONDS` (default
3600) to `expired` (treated as deny). Awaiting runs survive process restart
via `run_resume` + approval rows; the sweeper re-attaches on startup.
Metrics: `agent_approvals_total{status}`, time-to-decision histogram.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4301` |
| `FORGE_MODELS_URL` | `http://forge-models:4300` | Models base URL; must be absolute http(s) |
| `FORGE_CONTROL_URL` | `http://forge-control:4001` | Control (live tools) |
| `FORGE_RUNTIME_URL` | `http://forge-runtime:4102` | Runtime (live tools) |
| `FORGE_OBSERVE_URL` | `http://forge-observe:4106` | Observe (live tools) |
| `FORGE_STORAGE_URL` | `http://forge-storage:4107` | Storage (live tools) |
| `FORGE_EVENTS_URL` | `http://forge-events:4105` | Events (live tools) |
| `FORGE_AGENTS_DEFS_DIR` | packaged `agents/` | Directory of `*.yaml` / `*.yml` agent definitions |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | `fake\|live` platform tool backends |
| `FORGE_AGENTS_TOOL_TIMEOUT_SECONDS` | `15` | Per-tool HTTP timeout (live) |
| `FORGE_AGENTS_DB_PATH` | `/data/agents/runs.db` | SQLite run + step + approval store |
| `FORGE_AGENTS_MAX_CONCURRENT_RUNS` | `4` | In-flight run cap (`429` when exceeded) |
| `FORGE_AGENTS_APPROVAL_TTL_SECONDS` | `3600` | Pending approval TTL before auto-expire |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-agents` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-agents/
├── agents/                 # YAML agent definitions (seeds + fixture-echo)
├── migrations/             # SQLite schema (runs, approvals, run_resume)
├── app/
│   ├── main.py             # FastAPI factory + lifespan + approval sweeper
│   ├── config.py           # pydantic-settings
│   ├── health.py           # /health/live, /health/ready
│   ├── logging.py          # JSON logs + X-Request-ID middleware
│   ├── permissions.py      # CallScope + PermissionChecker
│   ├── agents/             # models + YAML loader + registry
│   ├── approvals/          # ApprovalStore + metrics
│   ├── tools/              # registry, invoker, fake + platform adapters
│   │   ├── control.py      # deployment.read
│   │   ├── observe.py      # logs.search, metrics.query
│   │   ├── runtime.py      # runtime.restart (destructive)
│   │   ├── storage.py      # storage.get/put
│   │   ├── models.py       # models.generate/embed
│   │   ├── events.py       # events.publish
│   │   └── fixtures/       # deterministic fake responses
│   ├── run/                # RunEngine, RunStore, ModelClient
│   └── api/                # agents, tools, runs, approvals routes
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```

