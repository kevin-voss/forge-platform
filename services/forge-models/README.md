# forge-models

Python/FastAPI model-serving service (epic 14). Host port **4300**.

Skeleton (14.01): health, identity, structured JSON logs, Compose wiring. Inference
APIs arrive in later steps; epic gate is `make demo DEMO=14` (`demos/14-model-serving`).

## Local

```bash
# from repo root
make service-run SERVICE=forge-models
make service-test SERVICE=forge-models

# or inside this directory
make sync
make dev          # http://127.0.0.1:4300
make test-unit
make lint
```

### Smoke

```bash
curl -fsS localhost:4300/health/live
curl -fsS localhost:4300/health/ready
curl -fsS localhost:4300/
```

OpenAPI (canonical): [`contracts/openapi/forge-models.openapi.yaml`](../../contracts/openapi/forge-models.openapi.yaml).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4300` |
| `FORGE_MODELS_BACKEND` | `fake` | `fake` or `local`; unknown values fail startup |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-models` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-models/
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
