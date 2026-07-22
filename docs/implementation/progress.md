# Implementation progress

Statuses: `Not started` · `Planning` · `In progress` · `Blocked` · `Complete`

**Use `N` (1, 2, 3, …)** — see [`STEPS.md`](STEPS.md). Next: **`N = 50`**.

## Epics

| Epic | Title | Status | Notes |
|---|---|---|---|
| [00](epics/00-repository-foundation.md) | Repository foundation | Complete | Local Compose foundation + docs system |
| [01](epics/01-runtime-contract.md) | Runtime contract | Complete | 7 steps; five-language demo 01 + shared validator |
| [02](epics/02-forge-control.md) | Forge Control | Complete | 8/8 steps complete; demo 02 acceptance gate passed |
| [03](epics/03-forge-cli.md) | Forge CLI | Complete | 6/6 steps complete; demo 03 CLI control-plane acceptance gate passed |
| [04](epics/04-forge-runtime.md) | Forge Runtime | Complete | 8/8 steps complete; demo 04 runtime acceptance gate passed |
| [05](epics/05-forge-gateway.md) | Forge Gateway | Complete | 7/7 steps complete; demo 05 routed-service acceptance gate passed |
| [06](epics/06-forge-build.md) | Forge Build | Complete | 7/7 steps complete; demo 06 source-to-deployment acceptance gate passed |
| [07](epics/07-deployment-reconciliation.md) | Deployment reconciliation | Complete | 6/6 steps complete; demo 07 rolling-deployment acceptance gate passed |
| [08](epics/08-multi-node-scheduler.md) | Multi-node scheduler | Planning | 6 steps |
| [09](epics/09-forge-identity.md) | Forge Identity | Planning | 8 steps |
| [10](epics/10-forge-secrets.md) | Forge Secrets | Planning | 7 steps |
| [11](epics/11-forge-events.md) | Forge Events | Planning | 7 steps |
| [12](epics/12-forge-observe.md) | Forge Observe | Planning | 7 steps |
| [13](epics/13-forge-storage.md) | Forge Storage | Planning | 7 steps |
| [14](epics/14-forge-models.md) | Forge Models | Planning | 7 steps |
| [15](epics/15-forge-agents.md) | Forge Agents | Planning | 8 steps |
| [16](epics/16-forge-workflows.md) | Forge Workflows | Planning | 7 steps |
| [17](epics/17-forge-memory.md) | Forge Memory | Planning | 6 steps |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | Planning | 6 steps |
| [19](epics/19-full-platform-demo.md) | Full platform demo | Planning | 6 steps; capstone |

## Steps

| N | Title | Status | Commit | Notes |
|---:|---|---|---|---|
| — | [Initialize repository foundation](steps/00-repository-foundation/00.01-initialize-foundation.md) (foundation) | Complete |  | Pre-queue |
| **1** | [Document runtime contract](steps/01-runtime-contract/01.01-document-runtime-contract.md) | Complete |  | Docs + OpenAPI + log schema + ports `4201–4205` |
| **2** | [Shared contract test runner](steps/01-runtime-contract/01.02-contract-test-runner.md) | Complete |  | `tools/contract-validator` + fixture tests |
| **3** | [Go demo application](steps/01-runtime-contract/01.03-go-demo-app.md) | Complete |  | `demos/01-container-runtime` Go slice + validator |
| **4** | [Python demo application](steps/01-runtime-contract/01.04-python-demo-app.md) | Complete |  | `demo-python-api` on `4204` + Go regression |
| **5** | [Kotlin demo application](steps/01-runtime-contract/01.05-kotlin-demo-app.md) | Complete |  | `demo-kotlin-api` on `4202` + Go/Python regression |
| **6** | [Rust demo application](steps/01-runtime-contract/01.06-rust-demo-app.md) | Complete |  | `demo-rust-api` on `4203` + Go/Kotlin/Python regression |
| **7** | [Elixir demo and full five-language suite](steps/01-runtime-contract/01.07-elixir-demo-and-full-suite.md) | Complete |  | `demo-elixir-api` on `4205` + full five-language suite |
| **8** | [Service skeleton, health, Compose](steps/02-forge-control/02.01-service-skeleton.md) | Complete |  | Ktor skeleton, health, Compose on `4001` |
| **9** | [Domain model + Postgres migrations](steps/02-forge-control/02.02-domain-model-and-migrations.md) | Complete |  | Schema `control`, Flyway, Hikari, JDBC repos |
| **10** | [Projects & environments API](steps/02-forge-control/02.03-projects-environments-api.md) | Complete |  | Projects/environments HTTP API + provisional errors |
| **11** | [Applications & services API + relationship validation](steps/02-forge-control/02.04-applications-services-api.md) | Complete |  | Nested applications/services API + relationship validation and audit |
| **12** | [Deployments desired-state API + basic audit](steps/02-forge-control/02.05-deployments-desired-state-api.md) | Complete |  | Desired-state deployments API, hierarchy read model, and audit records |
| **13** | [Shared errors, OpenAPI, contract tests, idempotency](steps/02-forge-control/02.06-errors-openapi-contract-idempotency.md) | Complete |  | Canonical errors/request IDs, OpenAPI contract, and persisted idempotent creates |
| **14** | [Structured logs + OTEL](steps/02-forge-control/02.07-structured-logs-and-otel.md) | Complete |  | JSON log correlation, OTLP HTTP/DB spans, and request metrics |
| **15** | [Demo `02-control-plane` + epic gate](steps/02-forge-control/02.08-demo-control-plane-and-gate.md) | Complete |  | End-to-end HTTP hierarchy, error envelope, migrations, and restart durability |
| **16** | [CLI skeleton, profiles, endpoint config, global flags](steps/03-forge-cli/03.01-cli-skeleton-and-config.md) | Complete |  | Go Cobra CLI, secure XDG profiles, global config resolution, HTTP client factory |
| **17** | [`project` / `app` / `service` commands](steps/03-forge-cli/03.02-project-app-service-commands.md) | Complete |  | Typed Control client, resource commands, table/JSON output, and API error request IDs |
| **18** | [`deployment create|status`](steps/03-forge-cli/03.03-deployment-commands.md) | Complete |  | Deployment create/status/list, UUID idempotency keys, and typed Control client |
| **19** | [Table/JSON output, exit codes, timeouts, request IDs](steps/03-forge-cli/03.04-output-exit-codes-timeouts.md) | Complete |  | Stable table/JSON output, taxonomy, request cancellation, and request IDs |
| **20** | [Shell completion + non-interactive mode](steps/03-forge-cli/03.05-completion-and-non-interactive.md) | Complete |  | Bash/zsh/fish completion, profile/static value suggestions, and headless-safe `--no-input` |
| **21** | [Demo `03-cli-control` + gate](steps/03-forge-cli/03.06-demo-cli-control-and-gate.md) | Complete |  | CLI-only hierarchy recreate, JSON stability, exit code 3 gate |
| **22** | [Skeleton + Docker socket + health](steps/04-forge-runtime/04.01-skeleton-docker-socket-health.md) | Complete |  | Rust/Axum skeleton on `4102`, Docker socket ping readiness |
| **23** | [Node identity + registration/heartbeat](steps/04-forge-runtime/04.02-node-identity-registration-heartbeat.md) | Complete |  | Stable node id, `/v1/node` + heartbeat, `forge.node_id` labels |
| **24** | [Workload create/start (pull, env, ports, labels)](steps/04-forge-runtime/04.03-workload-create-start.md) | Complete |  | `POST/GET /v1/workloads`, pull/create/start, deterministic name/labels, host port |
| **25** | [Health probing + status model](steps/04-forge-runtime/04.04-health-probing-status-model.md) | Complete |  | Prober + status enum, `GET /v1/workloads/{id}/status`, rediscovery |
| **26** | [Log streaming](steps/04-forge-runtime/04.05-log-streaming.md) | Complete |  | Bounded + SSE follow; stdout/stderr demux; managed-only |
| **27** | [Stop/delete; no duplicate containers](steps/04-forge-runtime/04.06-stop-delete-no-duplicates.md) | Complete |  | Idempotent `POST`, graceful `DELETE`, managed-only |
| **28** | [Control integration (desired→actual)](steps/04-forge-runtime/04.07-control-integration.md) | Complete |  | Poll Control, converge containers, `/v1/node/state`, status push contract |
| **29** | [Demo `04-runtime` + gate](steps/04-forge-runtime/04.08-demo-runtime-and-gate.md) | Complete |  | CLI→Control→Runtime deploy, active/failed status, logs, delete, idempotency |
| **30** | [Skeleton + health](steps/05-forge-gateway/05.01-skeleton-and-health.md) | Complete |  | Go skeleton on `4000`, health, Compose, graceful SIGTERM |
| **31** | [Route table + reverse proxy core](steps/05-forge-gateway/05.02-route-table-and-proxy-core.md) | Complete |  | In-memory routes, RR proxy, `GET/PUT /admin/routes` |
| **32** | [Sync routes from Control](steps/05-forge-gateway/05.03-sync-routes-from-control.md) | Complete |  | Control `/v1/endpoints` + Runtime interim sync, `POST /admin/routes/refresh` |
| **33** | [Health-aware upstreams](steps/05-forge-gateway/05.04-health-aware-upstreams.md) | Complete |  | Ready filter, active/passive probes, `503 no_healthy_upstream` |
| **34** | [Request IDs, forwarded headers, timeouts](steps/05-forge-gateway/05.05-request-ids-headers-timeouts.md) | Complete |  | Request IDs, X-Forwarded-*/Forwarded, connect/response/overall timeouts → 504 |
| **35** | [WebSocket + SSE proxy](steps/05-forge-gateway/05.06-websocket-and-sse-proxy.md) | Complete |  | WS hijack+copy, SSE flush-through, idle timeouts, request-id/forwarded on streams |
| **36** | [Demo `05-routed-service` + gate](steps/05-forge-gateway/05.07-demo-routed-service-and-gate.md) | Complete |  | Hostname routing for Go/Rust/Python, request-id propagation, 503 on stop, dynamic route update |
| **37** | [Skeleton + Docker + workspace](steps/06-forge-build/06.01-skeleton-docker-workspace.md) | Complete |  | Go skeleton on `4103`, Docker socket ping readiness, workspace mgr |
| **38** | [`forge.yaml` schema + build OpenAPI](steps/06-forge-build/06.02-forge-yaml-schema-and-openapi.md) | Complete |  | Schema, OpenAPI, manifest parser, build DTOs |
| **39** | [Clone/checkout + docker build + streamed logs](steps/06-forge-build/06.03-clone-checkout-docker-build-logs.md) | Complete |  | Clone/checkout, docker build, streamed logs, worker pool |
| **40** | [Tag + push local registry `:5000`](steps/06-forge-build/06.04-tag-and-push-registry.md) | Complete |  | Deterministic tag/push to `:5000`, digest on build record, retries |
| **41** | [Build status + failure paths](steps/06-forge-build/06.05-build-status-and-failure-paths.md) | Complete |  | Durable status/phases, cancel, cleanup + restart recovery |
| **42** | [Control integration (image ref on service)](steps/06-forge-build/06.06-control-integration-image-ref.md) | Complete |  | Build→Control image record + optional auto-deploy |
| **43** | [Demo `06-source-to-deployment` + gate](steps/06-forge-build/06.07-demo-source-to-deployment-and-gate.md) | Complete |  | Fixture→build→registry→Control→Runtime→Gateway; failed build creates no deployment |
| **44** | [Desired/actual replica model + controller skeleton](steps/07-deployment-reconciliation/07.01-desired-actual-model-and-controller-skeleton.md) | Complete |  | Migration + `computePlan` + inert controller + `GET …/reconcile` |
| **45** | [Single-replica reconcile loop](steps/07-deployment-reconciliation/07.02-single-replica-reconcile-loop.md) | Complete |  | Idempotent start/stop/recreate via Runtime; max actions/tick |
| **46** | [Rolling update (start new → ready → shift → stop old)](steps/07-deployment-reconciliation/07.03-rolling-update.md) | Complete |  | Rolling planner + readiness gate + Gateway traffic shift; min-available invariant |
| **47** | [Unhealthy rollout → automatic rollback](steps/07-deployment-reconciliation/07.04-unhealthy-rollout-automatic-rollback.md) | Complete |  | Timeout + rollback to last healthy; `status`/`last_healthy_image` on reconcile |
| **48** | [Deployment history + controller restart safety](steps/07-deployment-reconciliation/07.05-deployment-history-and-restart-safety.md) | Complete |  | Append-only `deployment_events` + `GET …/history`; StartupRecovery adopts/GCs |
| **49** | [Demo `07-rolling-deployment` + epic gate](steps/07-deployment-reconciliation/07.06-demo-07-rolling-deployment.md) | Complete |  | Demo 07: v1→v2 zero-downtime roll + v3 auto-rollback; `PATCH` desired image; history assertions |
| **50** | [Scheduler module/service skeleton + placement APIs](steps/08-multi-node-scheduler/08.01-scheduler-skeleton-and-placement-apis.md) | Not started |  |  |
| **51** | [Multi-node registration, heartbeat, resource reporting](steps/08-multi-node-scheduler/08.02-node-registration-heartbeat-resources.md) | Not started |  |  |
| **52** | [First-fit and least-allocated placement strategies](steps/08-multi-node-scheduler/08.03-first-fit-and-least-allocated-strategies.md) | Not started |  |  |
| **53** | [Anti-affinity + pending queue](steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) | Not started |  |  |
| **54** | [Reschedule on node offline](steps/08-multi-node-scheduler/08.05-reschedule-on-node-offline.md) | Not started |  |  |
| **55** | [Demo `08-multi-node` + epic gate](steps/08-multi-node-scheduler/08.06-demo-08-multi-node.md) | Not started |  |  |
| **56** | [Skeleton + Compose + Postgres](steps/09-forge-identity/09.01-skeleton-compose-postgres.md) | Not started |  |  |
| **57** | [Users, orgs, memberships persistence](steps/09-forge-identity/09.02-users-orgs-memberships.md) | Not started |  |  |
| **58** | [Registration, login, sessions](steps/09-forge-identity/09.03-registration-login-sessions.md) | Not started |  |  |
| **59** | [Roles + project membership](steps/09-forge-identity/09.04-roles-and-project-membership.md) | Not started |  |  |
| **60** | [API tokens + service accounts + revocation](steps/09-forge-identity/09.05-api-tokens-service-accounts-revocation.md) | Not started |  |  |
| **61** | [Control authz middleware (end `FORGE_AUTH_MODE=dev` default)](steps/09-forge-identity/09.06-control-authz-middleware.md) | Not started |  |  |
| **62** | [CLI `forge login` + token profile](steps/09-forge-identity/09.07-cli-login-and-token-profile.md) | Not started |  |  |
| **63** | [Demo `09-platform-identity` + epic gate](steps/09-forge-identity/09.08-demo-09-platform-identity.md) | Not started |  |  |
| **64** | [Skeleton + encryption key bootstrap](steps/10-forge-secrets/10.01-skeleton-and-encryption-key-bootstrap.md) | Not started |  |  |
| **65** | [Encrypted store + key versioning + metadata APIs](steps/10-forge-secrets/10.02-encrypted-store-key-versioning-metadata.md) | Not started |  |  |
| **66** | [Config vs secrets APIs; project isolation](steps/10-forge-secrets/10.03-config-vs-secrets-and-project-isolation.md) | Not started |  |  |
| **67** | [Runtime injection at deploy](steps/10-forge-secrets/10.04-runtime-injection-at-deploy.md) | Not started |  |  |
| **68** | [CLI `forge secret` / `forge config`](steps/10-forge-secrets/10.05-cli-secret-and-config.md) | Not started |  |  |
| **69** | [Access audit + log masking](steps/10-forge-secrets/10.06-access-audit-and-log-masking.md) | Not started |  |  |
| **70** | [Demo `10-secrets` + epic gate](steps/10-forge-secrets/10.07-demo-10-secrets.md) | Not started |  |  |
| **71** | [Skeleton + NATS wiring](steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) | Not started |  |  |
| **72** | [Publish/subscribe API](steps/11-forge-events/11.02-publish-subscribe-api.md) | Not started |  |  |
| **73** | [Durable consumers, ack, retry](steps/11-forge-events/11.03-durable-consumers-ack-retry.md) | Not started |  |  |
| **74** | [DLQ + inspect APIs](steps/11-forge-events/11.04-dlq-and-inspect-apis.md) | Not started |  |  |
| **75** | [Event JSON Schemas](steps/11-forge-events/11.05-event-json-schemas.md) | Not started |  |  |
| **76** | [Idempotency keys + consumer identity](steps/11-forge-events/11.06-idempotency-keys-and-consumer-identity.md) | Not started |  |  |
| **77** | [Demo `11-event-driven` (Go producer → Elixir consumer) + gate](steps/11-forge-events/11.07-demo-11-event-driven.md) | Not started |  |  |
| **78** | [Skeleton + correlation API design](steps/12-forge-observe/12.01-skeleton-and-correlation-api-design.md) | Not started |  |  |
| **79** | [Instrumentation checklist on Control/Runtime/Gateway/Build](steps/12-forge-observe/12.02-instrumentation-checklist.md) | Not started |  |  |
| **80** | [Grafana dashboards (platform/service/deployment/runtime)](steps/12-forge-observe/12.03-grafana-dashboards.md) | Not started |  |  |
| **81** | [Log query/filter by project/deployment/request/trace ID](steps/12-forge-observe/12.04-log-query-and-filter.md) | Not started |  |  |
| **82** | [CLI `forge logs --follow`](steps/12-forge-observe/12.05-cli-logs-follow.md) | Not started |  |  |
| **83** | [Basic alert rules](steps/12-forge-observe/12.06-basic-alert-rules.md) | Not started |  |  |
| **84** | [Demo `12-observability` (one distributed trace) + gate](steps/12-forge-observe/12.07-demo-12-observability.md) | Not started |  |  |
| **85** | [Skeleton + local FS backend](steps/13-forge-storage/13.01-skeleton-local-fs-backend.md) | Not started |  |  |
| **86** | [Buckets + metadata + project isolation](steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) | Not started |  |  |
| **87** | [Streamed upload/download](steps/13-forge-storage/13.03-streamed-upload-download.md) | Not started |  |  |
| **88** | [SHA-256 + range requests](steps/13-forge-storage/13.04-sha256-range-requests.md) | Not started |  |  |
| **89** | [Signed tokens + expiry](steps/13-forge-storage/13.05-signed-tokens-expiry.md) | Not started |  |  |
| **90** | [Quotas + delete + restart durability](steps/13-forge-storage/13.06-quotas-delete-durability.md) | Not started |  |  |
| **91** | [Demo `13-object-storage` + gate](steps/13-forge-storage/13.07-demo-and-gate.md) | Not started |  |  |
| **92** | [Skeleton + Compose](steps/14-forge-models/14.01-skeleton-compose.md) | Not started |  |  |
| **93** | [Model registry + `GET /v1/models`](steps/14-forge-models/14.02-model-registry.md) | Not started |  |  |
| **94** | [Local embeddings adapter](steps/14-forge-models/14.03-local-embeddings-adapter.md) | Not started |  |  |
| **95** | [Generate/classify/summarize endpoints](steps/14-forge-models/14.04-generate-classify-summarize.md) | Not started |  |  |
| **96** | [Streaming + async jobs](steps/14-forge-models/14.05-streaming-async-jobs.md) | Not started |  |  |
| **97** | [Usage metrics + OpenAPI; optional CLI `forge model`](steps/14-forge-models/14.06-usage-metrics-openapi-cli.md) | Not started |  |  |
| **98** | [Demo `14-model-serving` + gate](steps/14-forge-models/14.07-demo-and-gate.md) | Not started |  |  |
| **99** | [Skeleton](steps/15-forge-agents/15.01-skeleton.md) | Not started |  |  |
| **100** | [Agent registry + YAML definitions](steps/15-forge-agents/15.02-agent-registry-yaml.md) | Not started |  |  |
| **101** | [Tool registry + per-call permission checks](steps/15-forge-agents/15.03-tool-registry-permissions.md) | Not started |  |  |
| **102** | [Run engine: max steps, timeouts, history](steps/15-forge-agents/15.04-run-engine.md) | Not started |  |  |
| **103** | [Platform tools](steps/15-forge-agents/15.05-platform-tools.md) | Not started |  |  |
| **104** | [Human approval for destructive tools](steps/15-forge-agents/15.06-human-approval.md) | Not started |  |  |
| **105** | [Seed agents + CLI `forge agent`](steps/15-forge-agents/15.07-seed-agents-cli.md) | Not started |  |  |
| **106** | [Demo `15-agent-runtime` + gate](steps/15-forge-agents/15.08-demo-and-gate.md) | Not started |  |  |
| **107** | [Skeleton OTP + health](steps/16-forge-workflows/16.01-skeleton-otp-health.md) | Not started |  |  |
| **108** | [Definitions + durable run state](steps/16-forge-workflows/16.02-definitions-durable-state.md) | Not started |  |  |
| **109** | [Step primitives](steps/16-forge-workflows/16.03-step-primitives.md) | Not started |  |  |
| **110** | [Event triggers + agent steps](steps/16-forge-workflows/16.04-event-triggers-agent-steps.md) | Not started |  |  |
| **111** | [Human approval across restarts](steps/16-forge-workflows/16.05-human-approval-restarts.md) | Not started |  |  |
| **112** | [Compensation/rollback via Control](steps/16-forge-workflows/16.06-compensation-rollback.md) | Not started |  |  |
| **113** | [Demo `16-agent-workflow` + gate](steps/16-forge-workflows/16.07-demo-and-gate.md) | Not started |  |  |
| **114** | [Skeleton + persistence](steps/17-forge-memory/17.01-skeleton-persistence.md) | Not started |  |  |
| **115** | [Collections + fixed-dim vectors + metadata](steps/17-forge-memory/17.02-collections-vectors-metadata.md) | Not started |  |  |
| **116** | [Upsert + cosine NN query](steps/17-forge-memory/17.03-upsert-cosine-nn.md) | Not started |  |  |
| **117** | [Namespace/ACL via Identity project scope](steps/17-forge-memory/17.04-namespace-acl.md) | Not started |  |  |
| **118** | [Models embed + Agents retrieval tool](steps/17-forge-memory/17.05-models-embed-agents-tool.md) | Not started |  |  |
| **119** | [Demo `17-agent-memory` + gate](steps/17-forge-memory/17.06-demo-and-gate.md) | Not started |  |  |
| **120** | [Control APIs + provisioner skeleton](steps/18-managed-postgresql/18.01-control-apis-provisioner-skeleton.md) | Not started |  |  |
| **121** | [Create instance/database/credentials](steps/18-managed-postgresql/18.02-create-instance-db-credentials.md) | Not started |  |  |
| **122** | [Attach + Secrets/Runtime URL injection](steps/18-managed-postgresql/18.03-attach-secrets-runtime-injection.md) | Not started |  |  |
| **123** | [Backup + restore](steps/18-managed-postgresql/18.04-backup-restore.md) | Not started |  |  |
| **124** | [Credential rotation + deletion protection](steps/18-managed-postgresql/18.05-rotation-deletion-protection.md) | Not started |  |  |
| **125** | [CLI `forge database *` + demo + gate](steps/18-managed-postgresql/18.06-cli-demo-and-gate.md) | Not started |  |  |
| **126** | [Polyglot sample product](steps/19-full-platform-demo/19.01-polyglot-product-scaffold.md) | Not started |  |  |
| **127** | [Deploy path: Build→Runtime→Gateway→Events](steps/19-full-platform-demo/19.02-deploy-path.md) | Not started |  |  |
| **128** | [Identity, Secrets, Observe, Storage, managed DB](steps/19-full-platform-demo/19.03-identity-secrets-observe-storage-db.md) | Not started |  |  |
| **129** | [Models + Agents + Memory for diagnosis](steps/19-full-platform-demo/19.04-models-agents-memory.md) | Not started |  |  |
| **130** | [Failure injection + Workflows approval/rollback](steps/19-full-platform-demo/19.05-failure-injection-workflow.md) | Not started |  |  |
| **131** | [`demos/09-full-platform` acceptance suite + docs](steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) | Not started |  |  |

> Implementable steps: **131** (`N = 1` … `N = 131`). Foundation complete separately.

