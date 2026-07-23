# forge-agents

Python/FastAPI agent runtime service (epic 15). Host port **4301**.

Skeleton (15.01): health, identity, structured JSON logs, Compose wiring. Agent
registry, tools, and run engine arrive in later steps; epic gate is
`make demo DEMO=15` (`demos/15-agent-runtime`).

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
```

OpenAPI (canonical): [`contracts/openapi/forge-agents.openapi.yaml`](../../contracts/openapi/forge-agents.openapi.yaml).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4301` |
| `FORGE_MODELS_URL` | `http://forge-models:4300` | Models base URL (used from 15.04); must be absolute http(s) |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-agents` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-agents/
├── app/
│   ├── main.py      # FastAPI factory + lifespan
│   ├── config.py    # pydantic-settings
│   ├── health.py    # /health/live, /health/ready
│   └── logging.py   # JSON logs + X-Request-ID middleware
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```
