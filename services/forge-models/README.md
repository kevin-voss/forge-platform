# forge-models

Python/FastAPI model-serving service (epic 14). Host port **4300**.

Skeleton (14.01) + model registry (14.02): health, identity, structured JSON logs,
`GET /v1/models` (+ get/health). Inference adapters arrive in later steps; epic
gate is `make demo DEMO=14` (`demos/14-model-serving`).

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
curl -fsS localhost:4300/v1/models
curl -fsS localhost:4300/v1/models/local-embed-small
curl -fsS localhost:4300/v1/models/local-embed-small/health
```

OpenAPI (canonical): [`contracts/openapi/forge-models.openapi.yaml`](../../contracts/openapi/forge-models.openapi.yaml).

## Model registry

Registry entries load from `app/models.yaml` (override with `FORGE_MODELS_CONFIG`).
Each model is backed by a `ModelAdapter`. In 14.02 both `fake` and `local` backends
use `FakeAdapter` as a placeholder (capabilities + health only; no inference yet).

| Field | Notes |
|---|---|
| `id` | Stable model identifier |
| `capabilities` | subset of `embed`, `generate`, `classify`, `summarize` |
| `backend` | `fake` or `local` |
| `embedding_dim` | required when `embed` is listed; otherwise omitted/`null` |
| `status` | live adapter health: `ok`, `degraded`, or `down` |

Malformed `models.yaml` fails process startup with a clear `RegistryLoadError`.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4300` |
| `FORGE_MODELS_BACKEND` | `fake` | Default backend family (`fake`\|`local`); unknown values fail startup |
| `FORGE_MODELS_CONFIG` | packaged `app/models.yaml` | Path to registry definitions |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-models` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-models/
├── app/
│   ├── main.py           # FastAPI factory + lifespan
│   ├── config.py         # pydantic-settings
│   ├── health.py         # /health/live, /health/ready
│   ├── logging.py        # JSON logs + X-Request-ID middleware
│   ├── registry.py       # models.yaml loader + in-memory registry
│   ├── models.yaml       # default model definitions
│   ├── api/models.py     # GET /v1/models*
│   └── adapters/         # ModelAdapter + FakeAdapter
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```
