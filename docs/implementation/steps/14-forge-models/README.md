# Steps for epic 14-forge-models

Epic: [`../../epics/14-forge-models.md`](../../epics/14-forge-models.md) · Status: **In progress**

Unified model serving (Python, `services/forge-models`, host port `4300`, demo `demos/14-model-serving`).

| Step | Title | Status | Depends on |
|---|---|---|---|
| [14.01](14.01-skeleton-compose.md) | Skeleton + Compose | Complete | 00, 01 |
| [14.02](14.02-model-registry.md) | Model registry + `GET /v1/models` | Complete | 14.01 |
| [14.03](14.03-local-embeddings-adapter.md) | Local embeddings adapter | Complete | 14.02 |
| [14.04](14.04-generate-classify-summarize.md) | Generate/classify/summarize endpoints | Not started | 14.03 |
| [14.05](14.05-streaming-async-jobs.md) | Streaming + async jobs | Not started | 14.04 |
| [14.06](14.06-usage-metrics-openapi-cli.md) | Usage metrics + OpenAPI; optional CLI | Not started | 14.05, 12 |
| [14.07](14.07-demo-and-gate.md) | Demo `14-model-serving` + gate | Not started | 14.06 |

Next to implement: **14.04**.
