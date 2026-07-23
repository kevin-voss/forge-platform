# Implementation progress

Statuses: `Not started` · `Planning` · `In progress` · `Blocked` · `Complete`

**Use `N` (1, 2, 3, …)** — see [`STEPS.md`](STEPS.md). Next: **future queue `N = 133`** (epic 20; current roadmap 1–131 complete).

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
| [08](epics/08-multi-node-scheduler.md) | Multi-node scheduler | Complete | 6/6 steps complete; demo 08 multi-node acceptance gate passed |
| [09](epics/09-forge-identity.md) | Forge Identity | Complete | 8/8 steps complete; demo 09 platform-identity acceptance gate passed; default auth enforce |
| [10](epics/10-forge-secrets.md) | Forge Secrets | Complete | 7/7 steps; demo 10 secrets acceptance gate passed |
| [11](epics/11-forge-events.md) | Forge Events | Complete | 7/7 steps; demo 11 event-driven acceptance gate passed |
| [12](epics/12-forge-observe.md) | Forge Observe | Complete | 7/7 steps; demo 12 observability acceptance gate passed |
| [13](epics/13-forge-storage.md) | Forge Storage | Complete | 7/7 steps; demo 13 object-storage acceptance gate passed |
| [14](epics/14-forge-models.md) | Forge Models | Complete | 7/7 steps; demo 14 model-serving acceptance gate passed |
| [15](epics/15-forge-agents.md) | Forge Agents | Complete | 8/8 steps; demo 15 agent-runtime acceptance gate passed |
| [16](epics/16-forge-workflows.md) | Forge Workflows | Complete | 7/7 steps; demo 16 agent-workflow acceptance gate passed |
| [17](epics/17-forge-memory.md) | Forge Memory | Complete | 6/6 steps; demo 17 agent-memory acceptance gate passed |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | Complete | 6/6 steps; demo 18 managed-database acceptance gate passed |
| [19](epics/19-full-platform-demo.md) | Full platform demo | Complete | 6/6 steps; north-star gate `demos/09-full-platform` |

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
| **50** | [Scheduler module/service skeleton + placement APIs](steps/08-multi-node-scheduler/08.01-scheduler-skeleton-and-placement-apis.md) | Complete |  | Scheduler seam, SingleNodeScheduler, placements table + POST/GET APIs; reconciler records node |
| **51** | [Multi-node registration, heartbeat, resource reporting](steps/08-multi-node-scheduler/08.02-node-registration-heartbeat-resources.md) | Complete |  | Nodes table, register/heartbeat/list APIs, liveness monitor, Runtime capacity reporting |
| **52** | [First-fit and least-allocated placement strategies](steps/08-multi-node-scheduler/08.03-first-fit-and-least-allocated-strategies.md) | Complete |  | FirstFit + LeastAllocated strategies, CapacityReservation, FORGE_SCHEDULER_STRATEGY |
| **53** | [Anti-affinity + pending queue](steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) | Complete |  | Soft/hard anti-affinity spread; pending queue + FIFO drain; POST 202 |
| **54** | [Reschedule on node offline](steps/08-multi-node-scheduler/08.05-reschedule-on-node-offline.md) | Complete |  | Lost→reschedule/pending; grace flap suppression; stale replica fencer |
| **55** | [Demo `08-multi-node` + epic gate](steps/08-multi-node-scheduler/08.06-demo-08-multi-node.md) | Complete |  | Demo 08: 2+2 distribute + node-b stop reschedule; PlacementAwareRuntimeClient |
| **56** | [Skeleton + Compose + Postgres](steps/09-forge-identity/09.01-skeleton-compose-postgres.md) | Complete |  | Ktor skeleton on `4002`, Flyway baseline, `forge_identity` DB, Compose + OpenAPI |
| **57** | [Users, orgs, memberships persistence](steps/09-forge-identity/09.02-users-orgs-memberships.md) | Complete |  | Users/orgs/projects + memberships; citext email uniqueness; OpenAPI + TenancyTest |
| **58** | [Registration, login, sessions](steps/09-forge-identity/09.03-registration-login-sessions.md) | Complete |  | Argon2id credentials; opaque sessions; register/login/introspect/logout + lockout |
| **59** | [Roles + project membership](steps/09-forge-identity/09.04-roles-and-project-membership.md) | Complete |  | Role enum + permission matrix; authz/check + matrix APIs; AuthzMatrixTest |
| **60** | [API tokens + service accounts + revocation](steps/09-forge-identity/09.05-api-tokens-service-accounts-revocation.md) | Complete |  | Hashed API tokens + service accounts; introspect sessions/tokens; revoke |
| **61** | [Control authz middleware (end `FORGE_AUTH_MODE=dev` default)](steps/09-forge-identity/09.06-control-authz-middleware.md) | Complete |  | AuthMiddleware + IdentityClient; default `FORGE_AUTH_MODE=enforce`; 401/403; AuthMiddlewareTest |
| **62** | [CLI `forge login` + token profile](steps/09-forge-identity/09.07-cli-login-and-token-profile.md) | Complete |  | `forge login`/`whoami`/`logout`; credential store; Bearer on Control calls |
| **63** | [Demo `09-platform-identity` + epic gate](steps/09-forge-identity/09.08-demo-09-platform-identity.md) | Complete |  | Demo 09: enforce-mode identity gate; developer 201 / viewer 403 / revoke 401 |
| **64** | [Skeleton + encryption key bootstrap](steps/10-forge-secrets/10.01-skeleton-and-encryption-key-bootstrap.md) | Complete |  | Rust/Axum on `4104`, `KeyProvider` + AES-GCM wrap, `forge_secrets` DB |
| **65** | [Encrypted store + key versioning + metadata APIs](steps/10-forge-secrets/10.02-encrypted-store-key-versioning-metadata.md) | Complete |  | AEAD ciphertext+nonce; versions; list/metadata; `:access` reveal |
| **66** | [Config vs secrets APIs; project isolation](steps/10-forge-secrets/10.03-config-vs-secrets-and-project-isolation.md) | Complete |  | Config CRUD (values returned); SecretsAuth + Identity; project isolation 401/403 |
| **67** | [Runtime injection at deploy](steps/10-forge-secrets/10.04-runtime-injection-at-deploy.md) | Complete |  | Bindings + resolve env bundle; Control injects at StartReplica; fingerprint redeploy; Runtime masks env |
| **68** | [CLI `forge secret` / `forge config`](steps/10-forge-secrets/10.05-cli-secret-and-config.md) | Complete |  | `forge secret set/list/rotate` + `forge config set/show`; SecretsClient; no-echo/stdin/file |
| **69** | [Access audit + log masking](steps/10-forge-secrets/10.06-access-audit-and-log-masking.md) | Complete |  | `audit_events` + AuditRecorder; GET /audit; MaskingMakeWriter + masking lib; denied→result=denied |
| **70** | [Demo `10-secrets` + epic gate](steps/10-forge-secrets/10.07-demo-10-secrets.md) | Complete |  | Demo 10: set/rotate/redeploy; metadata-only list; log masking; epic gate |
| **71** | [Skeleton + NATS wiring](steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) | Complete |  | Go skeleton on `4105`; JetStream connect + platform stream bootstrap; ready gated on streams |
| **72** | [Publish/subscribe API](steps/11-forge-events/11.02-publish-subscribe-api.md) | Complete |  | `POST /v1/events` + `POST /v1/consume`; envelope; subject allow-list; OpenAPI |
| **73** | [Durable consumers, ack, retry](steps/11-forge-events/11.03-durable-consumers-ack-retry.md) | Complete |  | Named durables; explicit ack/nak; ack_wait redelivery; max_deliveries park |
| **74** | [DLQ + inspect APIs](steps/11-forge-events/11.04-dlq-and-inspect-apis.md) | Complete |  | `dlq_*` streams; terminal→DLQ; list/detail/redeliver/delete APIs |
| **75** | [Event JSON Schemas](steps/11-forge-events/11.05-event-json-schemas.md) | Complete |  | `contracts/events/*`; publish validates → 422; `GET /v1/schemas` |
| **76** | [Idempotency keys + consumer identity](steps/11-forge-events/11.06-idempotency-keys-and-consumer-identity.md) | Complete |  | Idempotency-Key→msg-id; processed_events seen store; consumer identity + optional auth |
| **77** | [Demo `11-event-driven` (Go producer → Elixir consumer) + gate](steps/11-forge-events/11.07-demo-11-event-driven.md) | Complete |  | Demo 11: Go→Elixir; schema 422; poison→DLQ; idempotency; epic gate |
| **78** | [Skeleton + correlation API design](steps/12-forge-observe/12.01-skeleton-and-correlation-api-design.md) | Complete |  | Go skeleton on `4106`; Loki/Tempo/Prometheus clients; correlation contract |
| **79** | [Instrumentation checklist on Control/Runtime/Gateway/Build](steps/12-forge-observe/12.02-instrumentation-checklist.md) | Complete |  | Checklist + OTEL correlation on Control/Runtime/Gateway/Build |
| **80** | [Grafana dashboards (platform/service/deployment/runtime)](steps/12-forge-observe/12.03-grafana-dashboards.md) | Complete |  | Four Grafana dashboards as code; provider + parity/smoke tests |
| **81** | [Log query/filter by project/deployment/request/trace ID](steps/12-forge-observe/12.04-log-query-and-filter.md) | Complete |  | `GET /v1/logs` LogQL filters; caps; authz; OpenAPI |
| **82** | [CLI `forge logs --follow`](steps/12-forge-observe/12.05-cli-logs-follow.md) | Complete |  | `forge logs` query + `--follow` SSE; reconnect; Runtime fallback |
| **83** | [Basic alert rules](steps/12-forge-observe/12.06-basic-alert-rules.md) | Complete |  | Prom rules + AM webhook sink; Observe `GET /v1/alerts`; platform alert panels |
| **84** | [Demo `12-observability` (one distributed trace) + gate](steps/12-forge-observe/12.07-demo-12-observability.md) | Complete |  | Distributed trace + logs + `forge logs --follow` + HighErrorRate gate |
| **85** | [Skeleton + local FS backend](steps/13-forge-storage/13.01-skeleton-local-fs-backend.md) | Complete |  | Rust/Axum on `4107`, `LocalFsBackend`, Compose volume `forge-storage-data` |
| **86** | [Buckets + metadata + project isolation](steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) | Complete |  | SQLite `meta/index.db`; bucket CRUD; `X-Forge-Project` / Identity isolation |
| **87** | [Streamed upload/download](steps/13-forge-storage/13.03-streamed-upload-download.md) | Complete |  | Streamed PUT/GET/HEAD; temp→atomic rename; bounded buffer |
| **88** | [SHA-256 + range requests](steps/13-forge-storage/13.04-sha256-range-requests.md) | Complete |  | Content-addressed SHA-256; ETag; Range 206/416; verify-on-read |
| **89** | [Signed tokens + expiry](steps/13-forge-storage/13.05-signed-tokens-expiry.md) | Complete |  | HMAC signed tokens; query/Bearer auth; expiry + TTL clamp |
| **90** | [Quotas + delete + restart durability](steps/13-forge-storage/13.06-quotas-delete-durability.md) | Complete |  | Per-project quota 413; DELETE + cascade; usage; boot reconcile |
| **91** | [Demo `13-object-storage` + gate](steps/13-forge-storage/13.07-demo-and-gate.md) | Complete |  | Demo 13: bucket→50MiB stream→checksum→range→expired token→delete→restart; epic gate |
| **92** | [Skeleton + Compose](steps/14-forge-models/14.01-skeleton-compose.md) | Complete |  | Python/FastAPI on `4300`, health/identity, JSON logs, Compose |
| **93** | [Model registry + `GET /v1/models`](steps/14-forge-models/14.02-model-registry.md) | Complete |  | Adapter interface + `models.yaml` registry; list/get/health |
| **94** | [Local embeddings adapter](steps/14-forge-models/14.03-local-embeddings-adapter.md) | Complete |  | Deterministic local embed + `POST /v1/models/{model}/embed` |
| **95** | [Generate/classify/summarize endpoints](steps/14-forge-models/14.04-generate-classify-summarize.md) | Complete |  | Deterministic local gen + `POST .../generate|classify|summarize` |
| **96** | [Streaming + async jobs](steps/14-forge-models/14.05-streaming-async-jobs.md) | Complete |  | SSE `generate?stream=true`; in-memory `/v1/jobs` submit/poll/cancel |
| **97** | [Usage metrics + OpenAPI; optional CLI `forge model`](steps/14-forge-models/14.06-usage-metrics-openapi-cli.md) | Complete |  | Prometheus `/metrics` + `/v1/usage`; OpenAPI lint; `forge model` CLI |
| **98** | [Demo `14-model-serving` + gate](steps/14-forge-models/14.07-demo-and-gate.md) | Complete |  | Demo 14: embed→classify→summarize + usage; epic gate |
| **99** | [Skeleton](steps/15-forge-agents/15.01-skeleton.md) | Complete |  | Python/FastAPI on `4301`, health/identity, JSON logs, Compose |
| **100** | [Agent registry + YAML definitions](steps/15-forge-agents/15.02-agent-registry-yaml.md) | Complete |  | YAML loader + `GET /v1/agents`; fixture-echo; limits bounds |
| **101** | [Tool registry + per-call permission checks](steps/15-forge-agents/15.03-tool-registry-permissions.md) | Complete |  | Tool registry + invoker; `GET /v1/tools`; deny reasons |
| **102** | [Run engine: max steps, timeouts, history](steps/15-forge-agents/15.04-run-engine.md) | Complete |  | Bounded model+tool loop; SQLite audit; dry-run; cancel |
| **103** | [Platform tools](steps/15-forge-agents/15.05-platform-tools.md) | Complete |  | Control/Runtime/Observe/Storage/Models/Events tools; fake fixtures; `runtime.restart` destructive |
| **104** | [Human approval for destructive tools](steps/15-forge-agents/15.06-human-approval.md) | Complete |  | Approval→`awaiting_approval`; approve/deny/expire; restart-safe |
| **105** | [Seed agents + CLI `forge agent`](steps/15-forge-agents/15.07-seed-agents-cli.md) | Complete |  | Five seed YAMLs; forge agent list/run/status/approve/deny; docs |
| **106** | [Demo `15-agent-runtime` + gate](steps/15-forge-agents/15.08-demo-and-gate.md) | Complete |  | Demo 15: investigator diagnose→awaiting_approval; epic gate |
| **107** | [Skeleton OTP + health](steps/16-forge-workflows/16.01-skeleton-otp-health.md) | Complete |  | Elixir/OTP on `4302`, health/identity, JSON logs, Compose |
| **108** | [Definitions + durable run state](steps/16-forge-workflows/16.02-definitions-durable-state.md) | Complete |  | YAML defs; Ecto runs/steps; resume + `(run_id,step_id)` idempotency |
| **109** | [Step primitives](steps/16-forge-workflows/16.03-step-primitives.md) | Complete |  | retry/delay/timeout/parallel/conditional; durable wake_at scheduler |
| **110** | [Event triggers + agent steps](steps/16-forge-workflows/16.04-event-triggers-agent-steps.md) | Complete |  | Durable Events consumer + event_dedup; agent step + fake/live client; `/v1/triggers/test` |
| **111** | [Human approval across restarts](steps/16-forge-workflows/16.05-human-approval-restarts.md) | Complete |  | `approval` step + ApprovalStore; awaiting_approval survives restart; approve/deny/expire |
| **112** | [Compensation/rollback via Control](steps/16-forge-workflows/16.06-compensation-rollback.md) | Complete |  | Saga log + reverse compensators; Control rollback client; report `rolled_back` |
| **113** | [Demo `16-agent-workflow` + gate](steps/16-forge-workflows/16.07-demo-and-gate.md) | Complete |  | Demo 16: event→diagnose→approve→rollback; restart-resume; epic gate |
| **114** | [Skeleton + persistence](steps/17-forge-memory/17.01-skeleton-persistence.md) | Complete |  | Rust/Axum on `4303`; health/identity; `vectors/`+`meta/` durable root |
| **115** | [Collections + fixed-dim vectors + metadata](steps/17-forge-memory/17.02-collections-vectors-metadata.md) | Complete |  | Collection CRUD; mmap `.vec` + SQLite meta; record get/list; dim `422` |
| **116** | [Upsert + cosine NN query](steps/17-forge-memory/17.03-upsert-cosine-nn.md) | Complete |  | Upsert/query/delete; cosine top-k + filters; boot compaction; ~27ms @10k |
| **117** | [Namespace/ACL via Identity project scope](steps/17-forge-memory/17.04-namespace-acl.md) | Complete |  | Project+namespace scope; Identity enforce; cross-project `404`; OpenAPI auth |
| **118** | [Models embed + Agents retrieval tool](steps/17-forge-memory/17.05-models-embed-agents-tool.md) | Complete |  | Text upsert/query via Models; `memory.search`/`memory.upsert` tools; dim `422` |
| **119** | [Demo `17-agent-memory` + gate](steps/17-forge-memory/17.06-demo-and-gate.md) | Complete |  | Demo 17: seed→NN→agent cite; isolation; restart; epic gate |
| **120** | [Control APIs + provisioner skeleton](steps/18-managed-postgresql/18.01-control-apis-provisioner-skeleton.md) | Complete |  | Managed-db schema + FakeProvisioner + create/list/get APIs |
| **121** | [Create instance/database/credentials](steps/18-managed-postgresql/18.02-create-instance-db-credentials.md) | Complete |  | LocalProvisioner containers; DB+role; Secrets `secret_ref`; isolation tests |
| **122** | [Attach + Secrets/Runtime URL injection](steps/18-managed-postgresql/18.03-attach-secrets-runtime-injection.md) | Complete |  | Attach APIs; URL in Secrets; reconciler injects env on deploy |
| **123** | [Backup + restore](steps/18-managed-postgresql/18.04-backup-restore.md) | Complete |  | On-demand `pg_dump`/restore; checksum; volume|storage archives; project-scoped APIs |
| **124** | [Credential rotation + deletion protection](steps/18-managed-postgresql/18.05-rotation-deletion-protection.md) | Complete |  | Rotation + Secrets update; deletion protection + force; pre-delete backup |
| **125** | [CLI `forge database *` + demo + gate](steps/18-managed-postgresql/18.06-cli-demo-and-gate.md) | Complete |  | `forge database *` CLI; demo 18 create→attach→deploy→backup→restore gate |
| **126** | [Polyglot sample product](steps/19-full-platform-demo/19.01-polyglot-product-scaffold.md) | Complete |  | Five product services under `demos/09-full-platform/product/`; contract + compose smoke |
| **127** | [Deploy path: Build→Runtime→Gateway→Events](steps/19-full-platform-demo/19.02-deploy-path.md) | Complete |  | Capstone compose + deploy.sh; Gateway hostnames; incident.created Events path; forge deployment create |
| **128** | [Identity, Secrets, Observe, Storage, managed DB](steps/19-full-platform-demo/19.03-identity-secrets-observe-storage-db.md) | Complete |  | setup-foundations.sh; Identity roles; Secrets+DB inject; Storage artifact; Tempo product trace |
| **129** | [Models + Agents + Memory for diagnosis](steps/19-full-platform-demo/19.04-models-agents-memory.md) | Complete |  | Capstone AI loop: Memory seed, investigator+memory.search, diagnosis cites telemetry+incident |
| **130** | [Failure injection + Workflows approval/rollback](steps/19-full-platform-demo/19.05-failure-injection-workflow.md) | Complete |  | CAPSTONE_BREAK readiness fail; incident-response workflow; approve→rollback+report; deny; mid-run resume |
| **131** | [`demos/09-full-platform` acceptance suite + docs](steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) | Complete |  | `start.sh`/`accept.sh`/`tests/`; CI subset + `make demo-accept DEMO=09-full-platform` north-star gate |

> Current-roadmap steps: **131** (`N = 1` … `N = 131`) — **complete**. Foundation complete separately.

---

# Future — standalone cloud (epics 20–43)

Planned work that begins **after** epic `19`. Nothing here affects the board above; the
next implementable step is still the one named at the top of this file. Plan:
[`FUTURE_PLAN.md`](FUTURE_PLAN.md) · architecture:
[`standalone-cloud.md`](../architecture/standalone-cloud.md).

## Future epics

| Epic | Title | Milestone | Status | Notes |
|---|---|---|---|---|
| [20](epics/20-declarative-resource-api.md) | Declarative resource API | M1 | In progress | 1/8 steps (`N = 132` complete); facade keeps epics 02/07 APIs intact |
| [21](epics/21-forge-discovery.md) | Forge Discovery | M1 | Planning | 6 steps (`N = 140`–`145`) |
| [22](epics/22-forge-network.md) | Forge Network | M1 | Planning | 7 steps (`N = 146`–`152`) |
| [23](epics/23-forge-infrastructure.md) | Forge Infrastructure | M1 | Planning | 7 steps (`N = 153`–`159`); Docker/SSH/bare-metal/Hetzner/AWS/Azure |
| [24](epics/24-forge-autoscaler.md) | Forge Autoscaler | M1 | Planning | 8 steps (`N = 160`–`167`); workload + worker + node scaling |
| [25](epics/25-scheduling-enhancements.md) | Scheduling enhancements | M1 | Planning | 6 steps (`N = 168`–`173`); M1 exit gate |
| [26](epics/26-forge-registry.md) | Forge Registry | M2 | Catalog | steps not yet materialized |
| [27](epics/27-deployment-strategies.md) | Deployment strategies | M2 | Catalog | canary, blue-green, traffic shifting |
| [28](epics/28-forge-queue.md) | Forge Queue | M2 | Catalog | job semantics over Forge Events |
| [29](epics/29-database-high-availability.md) | Database high availability | M2 | Catalog | standby, failover, read replicas, PITR |
| [30](epics/30-forge-volumes.md) | Forge Volumes | M2 | Catalog | provider-independent persistent volumes |
| [31](epics/31-distributed-object-storage.md) | Distributed object storage | M2 | Catalog | replication, repair, lifecycle |
| [32](epics/32-secrets-high-availability.md) | Secrets high availability | M2 | Catalog | envelope encryption, rotation |
| [33](epics/33-forge-policy.md) | Forge Policy | M2 | Catalog | admission, quotas, governance |
| [34](epics/34-dns-and-certificates.md) | DNS and certificates | M2 | Catalog | internal CA + ACME + domains |
| [35](epics/35-control-plane-high-availability.md) | Control-plane high availability | M2 | Catalog | leader election, leases, sharding |
| [36](epics/36-backup-and-disaster-recovery.md) | Backup and disaster recovery | M2 | Catalog | platform-wide backup + DR |
| [37](epics/37-alerts-and-incidents.md) | Alerts and incidents | M2 | Catalog | M2 exit gate |
| [38](epics/38-ai-infrastructure-scheduling.md) | AI infrastructure scheduling | M3 | Catalog | GPU scheduling, model scaling |
| [39](epics/39-multi-region.md) | Multi-region | M3 | Catalog | regions, residency, traffic steering |
| [40](epics/40-forge-console.md) | Forge Console | M3 | Catalog | public-API client only |
| [41](epics/41-usage-quotas-and-cost.md) | Usage, quotas, and cost | M3 | Catalog | metering + cost-aware scheduling |
| [42](epics/42-platform-upgrades.md) | Platform upgrades | M3 | Catalog | versioning, migrations, rollout |
| [43](epics/43-plugins-and-extensions.md) | Plugins and extensions | M3 | Catalog | M3 exit capstone |

## Future steps (M1)

| N | Title | Status | Commit | Notes |
|---:|---|---|---|---|
| **132** | [Resource envelope, kind registry, storage schema](steps/20-declarative-resource-api/20.01-resource-envelope-and-registry.md) | Complete |  | Envelope types, `KindRegistry`, `control.resources`, ULID ids; no HTTP yet |
| **133** | [Generic CRUD endpoints + optimistic concurrency](steps/20-declarative-resource-api/20.02-generic-crud-and-concurrency.md) | Not started |  | |
| **134** | [Generation tracking, status subresource, conditions](steps/20-declarative-resource-api/20.03-generation-status-and-conditions.md) | Not started |  | |
| **135** | [Labels, annotations, filtering, pagination](steps/20-declarative-resource-api/20.04-labels-selectors-and-listing.md) | Not started |  | |
| **136** | [Watch API + resource events](steps/20-declarative-resource-api/20.05-watch-api-and-resource-events.md) | Not started |  | |
| **137** | [Owner references, finalizers, terminating deletion](steps/20-declarative-resource-api/20.06-ownership-finalizers-and-deletion.md) | Not started |  | |
| **138** | [Compatibility facade for shipped APIs + `forge apply`](steps/20-declarative-resource-api/20.07-compat-facade-and-forge-apply.md) | Not started |  | |
| **139** | [Demo `20-declarative-resources` + epic gate](steps/20-declarative-resource-api/20.08-demo-20-declarative-resources.md) | Not started |  | |
| **140**–**145** | Epic 21 — Forge Discovery (`21.01`–`21.06`) | Not started |  | |
| **146**–**152** | Epic 22 — Forge Network (`22.01`–`22.07`) | Not started |  | |
| **153**–**159** | Epic 23 — Forge Infrastructure (`23.01`–`23.07`) | Not started |  | |
| **160**–**167** | Epic 24 — Forge Autoscaler (`24.01`–`24.08`) | Not started |  | |
| **168**–**173** | Epic 25 — Scheduling enhancements (`25.01`–`25.06`) | Not started |  | |

Per-step rows live in each epic's steps README; the global lookup is
[`STEPS.md`](STEPS.md#future-queue--standalone-cloud-epics-2025).

> Planned steps including the future queue: **173** (`N = 1` … `N = 173`).

