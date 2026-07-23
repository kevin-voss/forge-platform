# forge-models

Python/FastAPI model-serving service (epic 14). Host port **4300**.

Skeleton (14.01) + model registry (14.02) + local embeddings (14.03) +
generate/classify/summarize (14.04) + streaming/async jobs (14.05): health,
identity, structured JSON logs, `GET /v1/models`, `POST .../embed`,
synchronous `POST .../generate|classify|summarize`, SSE
`POST .../generate?stream=true`, and in-memory `POST|GET|DELETE /v1/jobs`.
Usage metrics arrive in 14.06; epic gate is `make demo DEMO=14`
(`demos/14-model-serving`).

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
curl -fsS -XPOST localhost:4300/v1/models/local-embed-small/embed \
  -H 'content-type: application/json' -d '{"input":"hello forge"}'
BASE=localhost:4300; M=local-general
curl -fsS -XPOST $BASE/v1/models/$M/generate -H 'content-type: application/json' \
  -d '{"prompt":"summarize: forge platform","max_tokens":32,"temperature":0}'
curl -fsS -XPOST $BASE/v1/models/$M/classify -H 'content-type: application/json' \
  -d '{"input":"database connection refused","labels":["network","auth","disk"]}'
curl -fsS -XPOST $BASE/v1/models/$M/summarize -H 'content-type: application/json' \
  -d '{"input":"long incident text about database connection refused ..."}'
curl -fsS -N -XPOST "$BASE/v1/models/$M/generate?stream=true" \
  -H 'content-type: application/json' -d '{"prompt":"stream please","max_tokens":16}' | head
JID=$(curl -fsS -XPOST $BASE/v1/jobs -H 'content-type: application/json' \
  -H 'X-Forge-Project: demo' \
  -d '{"model":"'$M'","task":"summarize","input":"async summary please"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["job_id"])')
sleep 1; curl -fsS $BASE/v1/jobs/$JID -H 'X-Forge-Project: demo' | grep -q '"status"'
```

OpenAPI (canonical): [`contracts/openapi/forge-models.openapi.yaml`](../../contracts/openapi/forge-models.openapi.yaml).

## Model registry

Registry entries load from `app/models.yaml` (override with `FORGE_MODELS_CONFIG`).
Each model is backed by a `ModelAdapter`:

| Capability | Adapter |
|---|---|
| `embed` | `LocalEmbeddingAdapter` (deterministic hash embedder; optional on-disk transformer) |
| `generate` / `classify` / `summarize` | `LocalGenerationAdapter` (deterministic fake for CI) |

| Field | Notes |
|---|---|
| `id` | Stable model identifier |
| `capabilities` | subset of `embed`, `generate`, `classify`, `summarize` |
| `backend` | `fake` or `local` |
| `embedding_dim` | required when `embed` is listed; otherwise omitted/`null` |
| `status` | live adapter health: `ok`, `degraded`, or `down` |

Malformed `models.yaml` fails process startup with a clear `RegistryLoadError`.
At startup, every embed adapter smoke-embeds once and asserts output dim ==
registry `embedding_dim`.

## Embeddings

`POST /v1/models/{model}/embed` accepts `{ "input": "text" }` or
`{ "input": ["a","b"] }` and returns:

```json
{
  "model": "local-embed-small",
  "embeddings": [[...]],
  "dim": 384,
  "usage": { "input_count": 1 }
}
```

CI uses a fully local deterministic hashing backend (no external API, no ML
deps). Set `FORGE_MODELS_LOCAL_MODEL_PATH` to an on-disk sentence-transformer
directory to enable a realistic local model for demos (requires
`sentence-transformers` installed in the image/env). Vectors are L2-normalized.

Validation errors return `422` with codes `invalid_input`, `batch_too_large`, or
`capability_unsupported`. Unknown models return `404` / `model_not_found`.

## Generation, classification, summarization

All three endpoints are served by `LocalGenerationAdapter` on `local-general`
(deterministic at `temperature=0`; no external API).

| Endpoint | Request | Response |
|---|---|---|
| `POST .../generate` | `{ "prompt", "max_tokens"?, "temperature"? }` | `{ "text", "finish_reason", "usage" }` |
| `POST .../generate?stream=true` | same | SSE `data: {"delta":"..."}` … `data: [DONE]` |
| `POST .../classify` | `{ "input", "labels": [...] }` | `{ "labels": [{ "label", "score" }] }` (score desc) |
| `POST .../summarize` | `{ "input", "max_tokens"?, "temperature"? }` | `{ "summary", "usage" }` |

`usage` is `{ prompt_tokens, completion_tokens, total_tokens }` (approximate word
counts for the fake adapter). Capability mismatches and invalid params return
`422` / `capability_unsupported` or `invalid_params`; unknown models → `404`.
Streams cancel work on client disconnect and close on
`FORGE_MODELS_STREAM_TIMEOUT_SECONDS`.

## Async jobs

In-memory only (not durable across restart). Scoped by `X-Forge-Project`
(cross-project read → `404`).

| Endpoint | Behavior |
|---|---|
| `POST /v1/jobs` | `{ model, task, input }` → `202 { job_id, status: queued }` |
| `GET /v1/jobs/{id}` | `{ status, result?, error? }` |
| `DELETE /v1/jobs/{id}` | cancel → `200 { status: cancelled }` or `409` if terminal |

`task` is one of `generate|classify|summarize|embed`. States:
`queued|running|succeeded|failed|cancelled`. Concurrency capped by
`FORGE_MODELS_MAX_CONCURRENT_JOBS`; timeout → `failed` / `timeout`.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | required (`8080` in Compose) | Listen port; host maps `4300` |
| `FORGE_MODELS_BACKEND` | `fake` | Default backend family (`fake`\|`local`); unknown values fail startup |
| `FORGE_MODELS_CONFIG` | packaged `app/models.yaml` | Path to registry definitions |
| `FORGE_MODELS_EMBED_MAX_BATCH` | `64` | Max texts per `/embed` request |
| `FORGE_MODELS_EMBED_MAX_CHARS` | `8192` | Max characters per input text |
| `FORGE_MODELS_GEN_MAX_TOKENS` | `512` | Cap for `max_tokens` on generate/summarize |
| `FORGE_MODELS_GEN_DEFAULT_TEMP` | `0.0` | Default temperature (deterministic) |
| `FORGE_MODELS_CLASSIFY_MAX_LABELS` | `32` | Max labels per `/classify` request |
| `FORGE_MODELS_STREAM_TIMEOUT_SECONDS` | `60` | SSE generate stream timeout |
| `FORGE_MODELS_JOB_TTL_SECONDS` | `3600` | GC TTL for terminal jobs |
| `FORGE_MODELS_MAX_CONCURRENT_JOBS` | `4` | Worker concurrency cap |
| `FORGE_MODELS_JOB_TIMEOUT_SECONDS` | `300` | Per-job wall timeout → `failed`/`timeout` |
| `FORGE_MODELS_LOCAL_MODEL_PATH` | _(empty)_ | Optional on-disk sentence-transformer path |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_SERVICE_NAME` | `forge-models` | Identity + log field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity payload |
| `FORGE_ENV` | `development` | Environment label |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

## Layout

```text
services/forge-models/
├── app/
│   ├── main.py                 # FastAPI factory + lifespan (job worker)
│   ├── config.py               # pydantic-settings
│   ├── health.py               # /health/live, /health/ready
│   ├── logging.py              # JSON logs + X-Request-ID middleware
│   ├── registry.py             # models.yaml loader + in-memory registry
│   ├── streaming.py            # SSE chunk helpers for generate?stream=true
│   ├── models.yaml             # default model definitions
│   ├── api/models.py           # GET /v1/models*
│   ├── api/embed.py            # POST /v1/models/{model}/embed
│   ├── api/generate.py         # POST /v1/models/{model}/generate
│   ├── api/classify.py         # POST /v1/models/{model}/classify
│   ├── api/summarize.py        # POST /v1/models/{model}/summarize
│   ├── api/jobs.py             # POST/GET/DELETE /v1/jobs
│   ├── jobs/                   # JobStore + background worker
│   └── adapters/               # ModelAdapter, FakeAdapter, LocalEmbedding*, LocalGeneration*
├── tests/
├── pyproject.toml
├── uv.lock
├── Dockerfile
└── Makefile
```
