# Implement queue (N = 1, 2, 3, …)

Change only **`N`** in [`IMPLEMENT_STEP.md`](IMPLEMENT_STEP.md). Do not use `01.01`-style ids in the prompt.

Foundation `00.01` is already complete and has no `N`.

| N | Title | Epic | Step doc |
|---:|---|---|---|
| **1** | Document runtime contract | 01 | [01.01-document-runtime-contract.md](steps/01-runtime-contract/01.01-document-runtime-contract.md) |
| **2** | Shared contract test runner | 01 | [01.02-contract-test-runner.md](steps/01-runtime-contract/01.02-contract-test-runner.md) |
| **3** | Go demo application | 01 | [01.03-go-demo-app.md](steps/01-runtime-contract/01.03-go-demo-app.md) |
| **4** | Python demo application | 01 | [01.04-python-demo-app.md](steps/01-runtime-contract/01.04-python-demo-app.md) |
| **5** | Kotlin demo application | 01 | [01.05-kotlin-demo-app.md](steps/01-runtime-contract/01.05-kotlin-demo-app.md) |
| **6** | Rust demo application | 01 | [01.06-rust-demo-app.md](steps/01-runtime-contract/01.06-rust-demo-app.md) |
| **7** | Elixir demo and full five-language suite | 01 | [01.07-elixir-demo-and-full-suite.md](steps/01-runtime-contract/01.07-elixir-demo-and-full-suite.md) |
| **8** | Service skeleton, health, Compose | 02 | [02.01-service-skeleton.md](steps/02-forge-control/02.01-service-skeleton.md) |
| **9** | Domain model + Postgres migrations | 02 | [02.02-domain-model-and-migrations.md](steps/02-forge-control/02.02-domain-model-and-migrations.md) |
| **10** | Projects & environments API | 02 | [02.03-projects-environments-api.md](steps/02-forge-control/02.03-projects-environments-api.md) |
| **11** | Applications & services API + relationship validation | 02 | [02.04-applications-services-api.md](steps/02-forge-control/02.04-applications-services-api.md) |
| **12** | Deployments desired-state API + basic audit | 02 | [02.05-deployments-desired-state-api.md](steps/02-forge-control/02.05-deployments-desired-state-api.md) |
| **13** | Shared errors, OpenAPI, contract tests, idempotency | 02 | [02.06-errors-openapi-contract-idempotency.md](steps/02-forge-control/02.06-errors-openapi-contract-idempotency.md) |
| **14** | Structured logs + OTEL | 02 | [02.07-structured-logs-and-otel.md](steps/02-forge-control/02.07-structured-logs-and-otel.md) |
| **15** | Demo `02-control-plane` + epic gate | 02 | [02.08-demo-control-plane-and-gate.md](steps/02-forge-control/02.08-demo-control-plane-and-gate.md) |
| **16** | CLI skeleton, profiles, endpoint config, global flags | 03 | [03.01-cli-skeleton-and-config.md](steps/03-forge-cli/03.01-cli-skeleton-and-config.md) |
| **17** | `project` / `app` / `service` commands | 03 | [03.02-project-app-service-commands.md](steps/03-forge-cli/03.02-project-app-service-commands.md) |
| **18** | `deployment create|status` | 03 | [03.03-deployment-commands.md](steps/03-forge-cli/03.03-deployment-commands.md) |
| **19** | Table/JSON output, exit codes, timeouts, request IDs | 03 | [03.04-output-exit-codes-timeouts.md](steps/03-forge-cli/03.04-output-exit-codes-timeouts.md) |
| **20** | Shell completion + non-interactive mode | 03 | [03.05-completion-and-non-interactive.md](steps/03-forge-cli/03.05-completion-and-non-interactive.md) |
| **21** | Demo `03-cli-control` + gate | 03 | [03.06-demo-cli-control-and-gate.md](steps/03-forge-cli/03.06-demo-cli-control-and-gate.md) |
| **22** | Skeleton + Docker socket + health | 04 | [04.01-skeleton-docker-socket-health.md](steps/04-forge-runtime/04.01-skeleton-docker-socket-health.md) |
| **23** | Node identity + registration/heartbeat | 04 | [04.02-node-identity-registration-heartbeat.md](steps/04-forge-runtime/04.02-node-identity-registration-heartbeat.md) |
| **24** | Workload create/start (pull, env, ports, labels) | 04 | [04.03-workload-create-start.md](steps/04-forge-runtime/04.03-workload-create-start.md) |
| **25** | Health probing + status model | 04 | [04.04-health-probing-status-model.md](steps/04-forge-runtime/04.04-health-probing-status-model.md) |
| **26** | Log streaming | 04 | [04.05-log-streaming.md](steps/04-forge-runtime/04.05-log-streaming.md) |
| **27** | Stop/delete; no duplicate containers | 04 | [04.06-stop-delete-no-duplicates.md](steps/04-forge-runtime/04.06-stop-delete-no-duplicates.md) |
| **28** | Control integration (desired→actual) | 04 | [04.07-control-integration.md](steps/04-forge-runtime/04.07-control-integration.md) |
| **29** | Demo `04-runtime` + gate | 04 | [04.08-demo-runtime-and-gate.md](steps/04-forge-runtime/04.08-demo-runtime-and-gate.md) |
| **30** | Skeleton + health | 05 | [05.01-skeleton-and-health.md](steps/05-forge-gateway/05.01-skeleton-and-health.md) |
| **31** | Route table + reverse proxy core | 05 | [05.02-route-table-and-proxy-core.md](steps/05-forge-gateway/05.02-route-table-and-proxy-core.md) |
| **32** | Sync routes from Control | 05 | [05.03-sync-routes-from-control.md](steps/05-forge-gateway/05.03-sync-routes-from-control.md) |
| **33** | Health-aware upstreams | 05 | [05.04-health-aware-upstreams.md](steps/05-forge-gateway/05.04-health-aware-upstreams.md) |
| **34** | Request IDs, forwarded headers, timeouts | 05 | [05.05-request-ids-headers-timeouts.md](steps/05-forge-gateway/05.05-request-ids-headers-timeouts.md) |
| **35** | WebSocket + SSE proxy | 05 | [05.06-websocket-and-sse-proxy.md](steps/05-forge-gateway/05.06-websocket-and-sse-proxy.md) |
| **36** | Demo `05-routed-service` + gate | 05 | [05.07-demo-routed-service-and-gate.md](steps/05-forge-gateway/05.07-demo-routed-service-and-gate.md) |
| **37** | Skeleton + Docker + workspace | 06 | [06.01-skeleton-docker-workspace.md](steps/06-forge-build/06.01-skeleton-docker-workspace.md) |
| **38** | `forge.yaml` schema + build OpenAPI | 06 | [06.02-forge-yaml-schema-and-openapi.md](steps/06-forge-build/06.02-forge-yaml-schema-and-openapi.md) |
| **39** | Clone/checkout + docker build + streamed logs | 06 | [06.03-clone-checkout-docker-build-logs.md](steps/06-forge-build/06.03-clone-checkout-docker-build-logs.md) |
| **40** | Tag + push local registry `:5000` | 06 | [06.04-tag-and-push-registry.md](steps/06-forge-build/06.04-tag-and-push-registry.md) |
| **41** | Build status + failure paths | 06 | [06.05-build-status-and-failure-paths.md](steps/06-forge-build/06.05-build-status-and-failure-paths.md) |
| **42** | Control integration (image ref on service) | 06 | [06.06-control-integration-image-ref.md](steps/06-forge-build/06.06-control-integration-image-ref.md) |
| **43** | Demo `06-source-to-deployment` + gate | 06 | [06.07-demo-source-to-deployment-and-gate.md](steps/06-forge-build/06.07-demo-source-to-deployment-and-gate.md) |
| **44** | Desired/actual replica model + controller skeleton | 07 | [07.01-desired-actual-model-and-controller-skeleton.md](steps/07-deployment-reconciliation/07.01-desired-actual-model-and-controller-skeleton.md) |
| **45** | Single-replica reconcile loop | 07 | [07.02-single-replica-reconcile-loop.md](steps/07-deployment-reconciliation/07.02-single-replica-reconcile-loop.md) |
| **46** | Rolling update (start new → ready → shift → stop old) | 07 | [07.03-rolling-update.md](steps/07-deployment-reconciliation/07.03-rolling-update.md) |
| **47** | Unhealthy rollout → automatic rollback | 07 | [07.04-unhealthy-rollout-automatic-rollback.md](steps/07-deployment-reconciliation/07.04-unhealthy-rollout-automatic-rollback.md) |
| **48** | Deployment history + controller restart safety | 07 | [07.05-deployment-history-and-restart-safety.md](steps/07-deployment-reconciliation/07.05-deployment-history-and-restart-safety.md) |
| **49** | Demo `07-rolling-deployment` + epic gate | 07 | [07.06-demo-07-rolling-deployment.md](steps/07-deployment-reconciliation/07.06-demo-07-rolling-deployment.md) |
| **50** | Scheduler module/service skeleton + placement APIs | 08 | [08.01-scheduler-skeleton-and-placement-apis.md](steps/08-multi-node-scheduler/08.01-scheduler-skeleton-and-placement-apis.md) |
| **51** | Multi-node registration, heartbeat, resource reporting | 08 | [08.02-node-registration-heartbeat-resources.md](steps/08-multi-node-scheduler/08.02-node-registration-heartbeat-resources.md) |
| **52** | First-fit and least-allocated placement strategies | 08 | [08.03-first-fit-and-least-allocated-strategies.md](steps/08-multi-node-scheduler/08.03-first-fit-and-least-allocated-strategies.md) |
| **53** | Anti-affinity + pending queue | 08 | [08.04-anti-affinity-and-pending-queue.md](steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) |
| **54** | Reschedule on node offline | 08 | [08.05-reschedule-on-node-offline.md](steps/08-multi-node-scheduler/08.05-reschedule-on-node-offline.md) |
| **55** | Demo `08-multi-node` + epic gate | 08 | [08.06-demo-08-multi-node.md](steps/08-multi-node-scheduler/08.06-demo-08-multi-node.md) |
| **56** | Skeleton + Compose + Postgres | 09 | [09.01-skeleton-compose-postgres.md](steps/09-forge-identity/09.01-skeleton-compose-postgres.md) |
| **57** | Users, orgs, memberships persistence | 09 | [09.02-users-orgs-memberships.md](steps/09-forge-identity/09.02-users-orgs-memberships.md) |
| **58** | Registration, login, sessions | 09 | [09.03-registration-login-sessions.md](steps/09-forge-identity/09.03-registration-login-sessions.md) |
| **59** | Roles + project membership | 09 | [09.04-roles-and-project-membership.md](steps/09-forge-identity/09.04-roles-and-project-membership.md) |
| **60** | API tokens + service accounts + revocation | 09 | [09.05-api-tokens-service-accounts-revocation.md](steps/09-forge-identity/09.05-api-tokens-service-accounts-revocation.md) |
| **61** | Control authz middleware (end `FORGE_AUTH_MODE=dev` default) | 09 | [09.06-control-authz-middleware.md](steps/09-forge-identity/09.06-control-authz-middleware.md) |
| **62** | CLI `forge login` + token profile | 09 | [09.07-cli-login-and-token-profile.md](steps/09-forge-identity/09.07-cli-login-and-token-profile.md) |
| **63** | Demo `09-platform-identity` + epic gate | 09 | [09.08-demo-09-platform-identity.md](steps/09-forge-identity/09.08-demo-09-platform-identity.md) |
| **64** | Skeleton + encryption key bootstrap | 10 | [10.01-skeleton-and-encryption-key-bootstrap.md](steps/10-forge-secrets/10.01-skeleton-and-encryption-key-bootstrap.md) |
| **65** | Encrypted store + key versioning + metadata APIs | 10 | [10.02-encrypted-store-key-versioning-metadata.md](steps/10-forge-secrets/10.02-encrypted-store-key-versioning-metadata.md) |
| **66** | Config vs secrets APIs; project isolation | 10 | [10.03-config-vs-secrets-and-project-isolation.md](steps/10-forge-secrets/10.03-config-vs-secrets-and-project-isolation.md) |
| **67** | Runtime injection at deploy | 10 | [10.04-runtime-injection-at-deploy.md](steps/10-forge-secrets/10.04-runtime-injection-at-deploy.md) |
| **68** | CLI `forge secret` / `forge config` | 10 | [10.05-cli-secret-and-config.md](steps/10-forge-secrets/10.05-cli-secret-and-config.md) |
| **69** | Access audit + log masking | 10 | [10.06-access-audit-and-log-masking.md](steps/10-forge-secrets/10.06-access-audit-and-log-masking.md) |
| **70** | Demo `10-secrets` + epic gate | 10 | [10.07-demo-10-secrets.md](steps/10-forge-secrets/10.07-demo-10-secrets.md) |
| **71** | Skeleton + NATS wiring | 11 | [11.01-skeleton-and-nats-wiring.md](steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) |
| **72** | Publish/subscribe API | 11 | [11.02-publish-subscribe-api.md](steps/11-forge-events/11.02-publish-subscribe-api.md) |
| **73** | Durable consumers, ack, retry | 11 | [11.03-durable-consumers-ack-retry.md](steps/11-forge-events/11.03-durable-consumers-ack-retry.md) |
| **74** | DLQ + inspect APIs | 11 | [11.04-dlq-and-inspect-apis.md](steps/11-forge-events/11.04-dlq-and-inspect-apis.md) |
| **75** | Event JSON Schemas | 11 | [11.05-event-json-schemas.md](steps/11-forge-events/11.05-event-json-schemas.md) |
| **76** | Idempotency keys + consumer identity | 11 | [11.06-idempotency-keys-and-consumer-identity.md](steps/11-forge-events/11.06-idempotency-keys-and-consumer-identity.md) |
| **77** | Demo `11-event-driven` (Go producer → Elixir consumer) + gate | 11 | [11.07-demo-11-event-driven.md](steps/11-forge-events/11.07-demo-11-event-driven.md) |
| **78** | Skeleton + correlation API design | 12 | [12.01-skeleton-and-correlation-api-design.md](steps/12-forge-observe/12.01-skeleton-and-correlation-api-design.md) |
| **79** | Instrumentation checklist on Control/Runtime/Gateway/Build | 12 | [12.02-instrumentation-checklist.md](steps/12-forge-observe/12.02-instrumentation-checklist.md) |
| **80** | Grafana dashboards (platform/service/deployment/runtime) | 12 | [12.03-grafana-dashboards.md](steps/12-forge-observe/12.03-grafana-dashboards.md) |
| **81** | Log query/filter by project/deployment/request/trace ID | 12 | [12.04-log-query-and-filter.md](steps/12-forge-observe/12.04-log-query-and-filter.md) |
| **82** | CLI `forge logs --follow` | 12 | [12.05-cli-logs-follow.md](steps/12-forge-observe/12.05-cli-logs-follow.md) |
| **83** | Basic alert rules | 12 | [12.06-basic-alert-rules.md](steps/12-forge-observe/12.06-basic-alert-rules.md) |
| **84** | Demo `12-observability` (one distributed trace) + gate | 12 | [12.07-demo-12-observability.md](steps/12-forge-observe/12.07-demo-12-observability.md) |
| **85** | Skeleton + local FS backend | 13 | [13.01-skeleton-local-fs-backend.md](steps/13-forge-storage/13.01-skeleton-local-fs-backend.md) |
| **86** | Buckets + metadata + project isolation | 13 | [13.02-buckets-metadata-project-isolation.md](steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) |
| **87** | Streamed upload/download | 13 | [13.03-streamed-upload-download.md](steps/13-forge-storage/13.03-streamed-upload-download.md) |
| **88** | SHA-256 + range requests | 13 | [13.04-sha256-range-requests.md](steps/13-forge-storage/13.04-sha256-range-requests.md) |
| **89** | Signed tokens + expiry | 13 | [13.05-signed-tokens-expiry.md](steps/13-forge-storage/13.05-signed-tokens-expiry.md) |
| **90** | Quotas + delete + restart durability | 13 | [13.06-quotas-delete-durability.md](steps/13-forge-storage/13.06-quotas-delete-durability.md) |
| **91** | Demo `13-object-storage` + gate | 13 | [13.07-demo-and-gate.md](steps/13-forge-storage/13.07-demo-and-gate.md) |
| **92** | Skeleton + Compose | 14 | [14.01-skeleton-compose.md](steps/14-forge-models/14.01-skeleton-compose.md) |
| **93** | Model registry + `GET /v1/models` | 14 | [14.02-model-registry.md](steps/14-forge-models/14.02-model-registry.md) |
| **94** | Local embeddings adapter | 14 | [14.03-local-embeddings-adapter.md](steps/14-forge-models/14.03-local-embeddings-adapter.md) |
| **95** | Generate/classify/summarize endpoints | 14 | [14.04-generate-classify-summarize.md](steps/14-forge-models/14.04-generate-classify-summarize.md) |
| **96** | Streaming + async jobs | 14 | [14.05-streaming-async-jobs.md](steps/14-forge-models/14.05-streaming-async-jobs.md) |
| **97** | Usage metrics + OpenAPI; optional CLI `forge model` | 14 | [14.06-usage-metrics-openapi-cli.md](steps/14-forge-models/14.06-usage-metrics-openapi-cli.md) |
| **98** | Demo `14-model-serving` + gate | 14 | [14.07-demo-and-gate.md](steps/14-forge-models/14.07-demo-and-gate.md) |
| **99** | Skeleton | 15 | [15.01-skeleton.md](steps/15-forge-agents/15.01-skeleton.md) |
| **100** | Agent registry + YAML definitions | 15 | [15.02-agent-registry-yaml.md](steps/15-forge-agents/15.02-agent-registry-yaml.md) |
| **101** | Tool registry + per-call permission checks | 15 | [15.03-tool-registry-permissions.md](steps/15-forge-agents/15.03-tool-registry-permissions.md) |
| **102** | Run engine: max steps, timeouts, history | 15 | [15.04-run-engine.md](steps/15-forge-agents/15.04-run-engine.md) |
| **103** | Platform tools | 15 | [15.05-platform-tools.md](steps/15-forge-agents/15.05-platform-tools.md) |
| **104** | Human approval for destructive tools | 15 | [15.06-human-approval.md](steps/15-forge-agents/15.06-human-approval.md) |
| **105** | Seed agents + CLI `forge agent` | 15 | [15.07-seed-agents-cli.md](steps/15-forge-agents/15.07-seed-agents-cli.md) |
| **106** | Demo `15-agent-runtime` + gate | 15 | [15.08-demo-and-gate.md](steps/15-forge-agents/15.08-demo-and-gate.md) |
| **107** | Skeleton OTP + health | 16 | [16.01-skeleton-otp-health.md](steps/16-forge-workflows/16.01-skeleton-otp-health.md) |
| **108** | Definitions + durable run state | 16 | [16.02-definitions-durable-state.md](steps/16-forge-workflows/16.02-definitions-durable-state.md) |
| **109** | Step primitives | 16 | [16.03-step-primitives.md](steps/16-forge-workflows/16.03-step-primitives.md) |
| **110** | Event triggers + agent steps | 16 | [16.04-event-triggers-agent-steps.md](steps/16-forge-workflows/16.04-event-triggers-agent-steps.md) |
| **111** | Human approval across restarts | 16 | [16.05-human-approval-restarts.md](steps/16-forge-workflows/16.05-human-approval-restarts.md) |
| **112** | Compensation/rollback via Control | 16 | [16.06-compensation-rollback.md](steps/16-forge-workflows/16.06-compensation-rollback.md) |
| **113** | Demo `16-agent-workflow` + gate | 16 | [16.07-demo-and-gate.md](steps/16-forge-workflows/16.07-demo-and-gate.md) |
| **114** | Skeleton + persistence | 17 | [17.01-skeleton-persistence.md](steps/17-forge-memory/17.01-skeleton-persistence.md) |
| **115** | Collections + fixed-dim vectors + metadata | 17 | [17.02-collections-vectors-metadata.md](steps/17-forge-memory/17.02-collections-vectors-metadata.md) |
| **116** | Upsert + cosine NN query | 17 | [17.03-upsert-cosine-nn.md](steps/17-forge-memory/17.03-upsert-cosine-nn.md) |
| **117** | Namespace/ACL via Identity project scope | 17 | [17.04-namespace-acl.md](steps/17-forge-memory/17.04-namespace-acl.md) |
| **118** | Models embed + Agents retrieval tool | 17 | [17.05-models-embed-agents-tool.md](steps/17-forge-memory/17.05-models-embed-agents-tool.md) |
| **119** | Demo `17-agent-memory` + gate | 17 | [17.06-demo-and-gate.md](steps/17-forge-memory/17.06-demo-and-gate.md) |
| **120** | Control APIs + provisioner skeleton | 18 | [18.01-control-apis-provisioner-skeleton.md](steps/18-managed-postgresql/18.01-control-apis-provisioner-skeleton.md) |
| **121** | Create instance/database/credentials | 18 | [18.02-create-instance-db-credentials.md](steps/18-managed-postgresql/18.02-create-instance-db-credentials.md) |
| **122** | Attach + Secrets/Runtime URL injection | 18 | [18.03-attach-secrets-runtime-injection.md](steps/18-managed-postgresql/18.03-attach-secrets-runtime-injection.md) |
| **123** | Backup + restore | 18 | [18.04-backup-restore.md](steps/18-managed-postgresql/18.04-backup-restore.md) |
| **124** | Credential rotation + deletion protection | 18 | [18.05-rotation-deletion-protection.md](steps/18-managed-postgresql/18.05-rotation-deletion-protection.md) |
| **125** | CLI `forge database *` + demo + gate | 18 | [18.06-cli-demo-and-gate.md](steps/18-managed-postgresql/18.06-cli-demo-and-gate.md) |
| **126** | Polyglot sample product | 19 | [19.01-polyglot-product-scaffold.md](steps/19-full-platform-demo/19.01-polyglot-product-scaffold.md) |
| **127** | Deploy path: Build→Runtime→Gateway→Events | 19 | [19.02-deploy-path.md](steps/19-full-platform-demo/19.02-deploy-path.md) |
| **128** | Identity, Secrets, Observe, Storage, managed DB | 19 | [19.03-identity-secrets-observe-storage-db.md](steps/19-full-platform-demo/19.03-identity-secrets-observe-storage-db.md) |
| **129** | Models + Agents + Memory for diagnosis | 19 | [19.04-models-agents-memory.md](steps/19-full-platform-demo/19.04-models-agents-memory.md) |
| **130** | Failure injection + Workflows approval/rollback | 19 | [19.05-failure-injection-workflow.md](steps/19-full-platform-demo/19.05-failure-injection-workflow.md) |
| **131** | `demos/09-full-platform` acceptance suite + docs | 19 | [19.06-acceptance-suite-and-gate.md](steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) |

Total implementable steps: **131**. Next: **`N = 10`**.
