# Epic 14: Forge Models

## Status

In progress

## Goal

Provide a unified, adapter-based model-serving API (Python, `services/forge-models`, host port `4300`) that offers embeddings, text generation, classification, and summarization over a stable HTTP surface, with at least one fully local backend that requires no external API in CI. When done, the service exposes a model registry, synchronous + streaming + async inference, and usage metrics, all proven by `demos/14-model-serving`.

## Why this epic exists

Later AI-native epics depend on model inference: Agents (15) call generate/classify, Memory (17) needs embeddings, and Workflows (16) orchestrate agent steps. Centralizing inference behind one contract with pluggable adapters means the rest of the platform never hardcodes a vendor and CI can run deterministically with a local backend.

## Primary code areas

* `services/forge-models/` — Python service (FastAPI recommended)
* `services/forge-models/adapters/` — model backend adapters (local first)
* `contracts/openapi/forge-models.openapi.yaml`
* `demos/14-model-serving/` — Go client demo + acceptance
* Optional: `tools/forge-cli` extension for `forge model` (thin client)

## Suggested language

Python (per `specs.md` §4 / Step 14). FastAPI + `uvicorn` recommended; adapters isolate any ML deps.

## Spec references

* `specs.md` → Step 14: Forge Models (capabilities, adapters, API, demo, tests, acceptance)
* `specs.md` → Step 12: Forge Observe (usage metrics)
* `specs.md` → Step 13: Forge Storage (optional model file storage)

## Dependencies

* Epic [`00-repository-foundation`](00-repository-foundation.md) complete
* Epic [`01-runtime-contract`](01-runtime-contract.md) conventions (health, logs, `PORT`, shutdown)
* Epic [`12-forge-observe`](12-forge-observe.md) for usage metrics surfacing (minimum: metrics endpoint/OTEL export path)
* Epic [`13-forge-storage`](13-forge-storage.md) optional (model file storage); not required for local fixture backend

## Out of scope for this epic

* Real external provider credentials/keys in CI (adapter interface only + optional local demo)
* GPU scheduling / model autoscaling
* Fine-tuning / training
* Vector storage (that is Memory, epic 17 — Models only produces embeddings)
* Client SDKs under `packages/*`

## Success demo

```bash
make demo DEMO=14
```

`demos/14-model-serving`: a Go client sends text to `forge-models` and receives an embedding, a classification, and a generated summary — all served by a local backend with no external API.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [14.01](../steps/14-forge-models/14.01-skeleton-compose.md) | Skeleton + Compose | Complete | Python/FastAPI, health, port 4300 |
| [14.02](../steps/14-forge-models/14.02-model-registry.md) | Model registry + `GET /v1/models` | Complete | Depends on 14.01; adapter registry |
| [14.03](../steps/14-forge-models/14.03-local-embeddings-adapter.md) | Local embeddings adapter | Complete | Deterministic local embed + `/embed`; no external API in CI |
| [14.04](../steps/14-forge-models/14.04-generate-classify-summarize.md) | Generate/classify/summarize endpoints | Not started | Depends on 14.03 |
| [14.05](../steps/14-forge-models/14.05-streaming-async-jobs.md) | Streaming + async jobs | Not started | Depends on 14.04 |
| [14.06](../steps/14-forge-models/14.06-usage-metrics-openapi-cli.md) | Usage metrics + OpenAPI; optional CLI | Not started | Depends on 14.05 |
| [14.07](../steps/14-forge-models/14.07-demo-and-gate.md) | Demo `14-model-serving` + gate | Not started | Depends on 14.06 |

## Assumptions

* Service at `services/forge-models/`, host port `4300`.
* CI backend is a deterministic local embeddings model (a small on-device sentence-transformer if bundled, else a deterministic hash-based stub) — MASTER_PLAN open question 5.
* Generation/classification/summarization in CI use a small local model or a deterministic fake adapter selected via `FORGE_MODELS_BACKEND=fake|local`.
* API shape follows `specs.md`: `POST /v1/models/{model}/generate|embed|classify`, `GET /v1/models`. Summarization is exposed as a generation preset (`/summarize` or a `task=summarize` parameter).
* Embedding dimension is fixed per model and reported in the registry (consumed by Memory epic 17).

## Open questions

* Local model choice: bundle a tiny HF model (heavier image, more realistic) vs deterministic stub (fast, fully reproducible). Assumption: stub for CI acceptance, real small model optional behind a flag for local demos.
* Summarization surface: dedicated `/summarize` endpoint vs generation with a summarize prompt/task. Assumption: `POST /v1/models/{model}/summarize` thin wrapper over generate.
* Async job persistence: in-memory vs durable (Storage/DB). Assumption: in-memory job store for this epic, documented as non-durable; durability optional later.
* Batching: request-level batching only, or micro-batching across requests? Assumption: accept batch inputs per request; cross-request micro-batching optional.

## Next step to implement

**[14.04](../steps/14-forge-models/14.04-generate-classify-summarize.md) — Generate/classify/summarize endpoints**.
