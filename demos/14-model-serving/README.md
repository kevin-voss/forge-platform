# Demo 14: Model serving

End-to-end acceptance gate for epic 14 (Forge Models). A standalone Compose
stack brings up `forge-models` on host port **4300** with
`FORGE_MODELS_BACKEND=fake`, then a Go client proves embed, classify, and
summarize against the documented local backends — with no external API.

```text
1. bring up forge-models (fake / local deterministic adapters)
2. Go client embeds text → assert vector length == model dim (384)
3. Go client classifies text with labels → assert a top label returned
4. Go client summarizes text → assert non-empty summary
5. GET /v1/usage shows the three calls; /metrics exposes counters
```

```text
go-client (Compose one-shot)
        │  HTTP
        ▼
forge-models :4300
   local-embed-small  → embed
   local-general      → classify + summarize
   /v1/usage + /metrics
```

## What this demo checks

* OpenAPI contract file parses and declares embed/classify/summarize/usage paths.
* Go assertion helpers (dim equality, label ordering, non-empty summary) unit-test
  green before the stack starts.
* A real HTTP client receives a valid embedding, classification, and summary
  from fully local adapters (`FORGE_MODELS_BACKEND=fake`).
* Usage is observable after the demo via `GET /v1/usage` and `GET /metrics`.
* Deterministic CI path: no external model provider credentials.

## Run

From the repository root:

```bash
make demo DEMO=14
```

Expect a final `demo 14 PASSED` line and exit code `0`. On failure the script
dumps a tail of `forge-models` logs plus `/v1/usage`, then tears down with
`docker compose down -v`.

Optional bring-up only (leaves the stack running):

```bash
./demos/14-model-serving/run.sh --phase=up
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_MODELS_URL` | `http://127.0.0.1:4300` | Host API + readiness |
| `FORGE_MODELS_BACKEND` | `fake` | Deterministic CI backend family |
| `FORGE_MODELS_EMBED_MODEL` | `local-embed-small` | Embed registry id |
| `FORGE_MODELS_GEN_MODEL` | `local-general` | Classify/summarize registry id |
| `FORGE_MODELS_METRICS_ENABLED` | `true` | Expose `/metrics` |
| `FORGE_LOG_LEVEL` | `info` | Service log level |

## Security notes

* No credentials or secrets; inference is fully local.
* Suitable for CI regression of the models contract.
