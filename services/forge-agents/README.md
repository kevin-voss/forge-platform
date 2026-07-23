# forge-agents

Python/FastAPI agent runtime service (epic 15). Host port **4301**.

Skeleton (15.01) plus agent registry (15.02): YAML definitions under `agents/`
load at startup into a validated in-memory registry exposed as
`GET /v1/agents` and `GET /v1/agents/{name}`. Tools, run engine, and epic gate
(`make demo DEMO=15`) arrive in later steps.

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
path and reason. Tool *existence* is enforced in 15.03.

Startup logs include `agents_registry_size` and loaded agent names.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4301` |
| `FORGE_MODELS_URL` | `http://forge-models:4300` | Models base URL (used from 15.04); must be absolute http(s) |
| `FORGE_AGENTS_DEFS_DIR` | packaged `agents/` | Directory of `*.yaml` / `*.yml` agent definitions |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-agents` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-agents/
├── agents/                 # YAML agent definitions (fixture-echo for tests)
├── app/
│   ├── main.py             # FastAPI factory + lifespan
│   ├── config.py           # pydantic-settings
│   ├── health.py           # /health/live, /health/ready
│   ├── logging.py          # JSON logs + X-Request-ID middleware
│   ├── agents/             # models + YAML loader + registry
│   └── api/agents.py       # GET /v1/agents
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```
