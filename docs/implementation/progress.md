# Implementation progress

Statuses: `Not started` · `Planning` · `In progress` · `Blocked` · `Complete`

Master catalog: [`MASTER_PLAN.md`](MASTER_PLAN.md). Next to implement: **`01.01`**.

## Epics

| Epic | Title | Status | Notes |
|---|---|---|---|
| [00](epics/00-repository-foundation.md) | Repository foundation | Complete | Local Compose foundation + docs system |
| [01](epics/01-runtime-contract.md) | Runtime contract | Planning | 7 steps planned; implement `01.01` next |
| [02](epics/02-forge-control.md) | Forge Control | Planning | 8 steps planned; after epic 01 |
| [03](epics/03-forge-cli.md) | Forge CLI | Planning | 6 steps planned; needs Control API |
| [04](epics/04-forge-runtime.md) | Forge Runtime | Planning | 8 steps planned |
| [05](epics/05-forge-gateway.md) | Forge Gateway | Planning | 7 steps planned |
| [06](epics/06-forge-build.md) | Forge Build | Planning | 7 steps planned |
| [07](epics/07-deployment-reconciliation.md) | Deployment reconciliation | Planning | 6 steps planned; cross-cutting |
| [08](epics/08-multi-node-scheduler.md) | Multi-node scheduler | Planning | 6 steps planned; cross-cutting |
| [09](epics/09-forge-identity.md) | Forge Identity | Planning | 8 steps planned |
| [10](epics/10-forge-secrets.md) | Forge Secrets | Planning | 7 steps planned |
| [11](epics/11-forge-events.md) | Forge Events | Planning | 7 steps planned |
| [12](epics/12-forge-observe.md) | Forge Observe | Planning | 7 steps planned |
| [13](epics/13-forge-storage.md) | Forge Storage | Planning | 7 steps planned |
| [14](epics/14-forge-models.md) | Forge Models | Planning | 7 steps planned |
| [15](epics/15-forge-agents.md) | Forge Agents | Planning | 8 steps planned |
| [16](epics/16-forge-workflows.md) | Forge Workflows | Planning | 7 steps planned |
| [17](epics/17-forge-memory.md) | Forge Memory | Planning | 6 steps planned |
| [18](epics/18-managed-postgresql.md) | Managed PostgreSQL | Planning | 6 steps planned |
| [19](epics/19-full-platform-demo.md) | Full platform demo | Planning | 6 steps planned; capstone `demos/09-full-platform` |

## Steps

| Step | Epic | Title | Status | Commit | Notes |
|---|---|---|---|---|---|
| [00.01](steps/00-repository-foundation/00.01-initialize-foundation.md) | 00 | Initialize repository foundation | Complete |  | Foundation complete |
| [01.01](steps/01-runtime-contract/01.01-document-runtime-contract.md) | 01 | Document runtime contract | Not started |  | Docs + OpenAPI + log schema — **implement next** |
| [01.02](steps/01-runtime-contract/01.02-contract-test-runner.md) | 01 | Shared contract test runner | Not started |  |  |
| [01.03](steps/01-runtime-contract/01.03-go-demo-app.md) | 01 | Go demo application | Not started |  |  |
| [01.04](steps/01-runtime-contract/01.04-python-demo-app.md) | 01 | Python demo application | Not started |  |  |
| [01.05](steps/01-runtime-contract/01.05-kotlin-demo-app.md) | 01 | Kotlin demo application | Not started |  |  |
| [01.06](steps/01-runtime-contract/01.06-rust-demo-app.md) | 01 | Rust demo application | Not started |  |  |
| [01.07](steps/01-runtime-contract/01.07-elixir-demo-and-full-suite.md) | 01 | Elixir demo and full five-language suite | Not started |  | Epic acceptance gate |
| [02.01](steps/02-forge-control/02.01-service-skeleton.md) | 02 | Service skeleton, health, Compose | Not started |  |  |
| [02.02](steps/02-forge-control/02.02-domain-model-and-migrations.md) | 02 | Domain model + Postgres migrations | Not started |  |  |
| [02.03](steps/02-forge-control/02.03-projects-environments-api.md) | 02 | Projects & environments API | Not started |  |  |
| [02.04](steps/02-forge-control/02.04-applications-services-api.md) | 02 | Applications & services API + relationship validation | Not started |  |  |
| [02.05](steps/02-forge-control/02.05-deployments-desired-state-api.md) | 02 | Deployments desired-state API + basic audit | Not started |  |  |
| [02.06](steps/02-forge-control/02.06-errors-openapi-contract-idempotency.md) | 02 | Shared errors, OpenAPI, contract tests, idempotency | Not started |  |  |
| [02.07](steps/02-forge-control/02.07-structured-logs-and-otel.md) | 02 | Structured logs + OTEL | Not started |  |  |
| [02.08](steps/02-forge-control/02.08-demo-control-plane-and-gate.md) | 02 | Demo `02-control-plane` + epic gate | Not started |  | Demo `02-control-plane` gate |
| [03.01](steps/03-forge-cli/03.01-cli-skeleton-and-config.md) | 03 | CLI skeleton, profiles, endpoint config, global flags | Not started |  |  |
| [03.02](steps/03-forge-cli/03.02-project-app-service-commands.md) | 03 | `project` / `app` / `service` commands | Not started |  |  |
| [03.03](steps/03-forge-cli/03.03-deployment-commands.md) | 03 | `deployment create|status` | Not started |  |  |
| [03.04](steps/03-forge-cli/03.04-output-exit-codes-timeouts.md) | 03 | Table/JSON output, exit codes, timeouts, request IDs | Not started |  |  |
| [03.05](steps/03-forge-cli/03.05-completion-and-non-interactive.md) | 03 | Shell completion + non-interactive mode | Not started |  |  |
| [03.06](steps/03-forge-cli/03.06-demo-cli-control-and-gate.md) | 03 | Demo `03-cli-control` + gate | Not started |  | Demo `03-cli-control` gate |
| [04.01](steps/04-forge-runtime/04.01-skeleton-docker-socket-health.md) | 04 | Skeleton + Docker socket + health | Not started |  |  |
| [04.02](steps/04-forge-runtime/04.02-node-identity-registration-heartbeat.md) | 04 | Node identity + registration/heartbeat | Not started |  |  |
| [04.03](steps/04-forge-runtime/04.03-workload-create-start.md) | 04 | Workload create/start (pull, env, ports, labels) | Not started |  |  |
| [04.04](steps/04-forge-runtime/04.04-health-probing-status-model.md) | 04 | Health probing + status model | Not started |  |  |
| [04.05](steps/04-forge-runtime/04.05-log-streaming.md) | 04 | Log streaming | Not started |  |  |
| [04.06](steps/04-forge-runtime/04.06-stop-delete-no-duplicates.md) | 04 | Stop/delete; no duplicate containers | Not started |  |  |
| [04.07](steps/04-forge-runtime/04.07-control-integration.md) | 04 | Control integration (desired→actual) | Not started |  |  |
| [04.08](steps/04-forge-runtime/04.08-demo-runtime-and-gate.md) | 04 | Demo `04-runtime` + gate | Not started |  | Demo `04-runtime` gate |
| [05.01](steps/05-forge-gateway/05.01-skeleton-and-health.md) | 05 | Skeleton + health | Not started |  |  |
| [05.02](steps/05-forge-gateway/05.02-route-table-and-proxy-core.md) | 05 | Route table + reverse proxy core | Not started |  |  |
| [05.03](steps/05-forge-gateway/05.03-sync-routes-from-control.md) | 05 | Sync routes from Control | Not started |  |  |
| [05.04](steps/05-forge-gateway/05.04-health-aware-upstreams.md) | 05 | Health-aware upstreams | Not started |  |  |
| [05.05](steps/05-forge-gateway/05.05-request-ids-headers-timeouts.md) | 05 | Request IDs, forwarded headers, timeouts | Not started |  |  |
| [05.06](steps/05-forge-gateway/05.06-websocket-and-sse-proxy.md) | 05 | WebSocket + SSE proxy | Not started |  |  |
| [05.07](steps/05-forge-gateway/05.07-demo-routed-service-and-gate.md) | 05 | Demo `05-routed-service` + gate | Not started |  | Demo `05-routed-service` gate |
| [06.01](steps/06-forge-build/06.01-skeleton-docker-workspace.md) | 06 | Skeleton + Docker + workspace | Not started |  |  |
| [06.02](steps/06-forge-build/06.02-forge-yaml-schema-and-openapi.md) | 06 | `forge.yaml` schema + build OpenAPI | Not started |  |  |
| [06.03](steps/06-forge-build/06.03-clone-checkout-docker-build-logs.md) | 06 | Clone/checkout + docker build + streamed logs | Not started |  |  |
| [06.04](steps/06-forge-build/06.04-tag-and-push-registry.md) | 06 | Tag + push local registry `:5000` | Not started |  |  |
| [06.05](steps/06-forge-build/06.05-build-status-and-failure-paths.md) | 06 | Build status + failure paths | Not started |  |  |
| [06.06](steps/06-forge-build/06.06-control-integration-image-ref.md) | 06 | Control integration (image ref on service) | Not started |  |  |
| [06.07](steps/06-forge-build/06.07-demo-source-to-deployment-and-gate.md) | 06 | Demo `06-source-to-deployment` + gate | Not started |  | Demo `06-source-to-deployment` gate |
| [07.01](steps/07-deployment-reconciliation/07.01-desired-actual-model-and-controller-skeleton.md) | 07 | Desired/actual replica model + controller skeleton | Not started |  |  |
| [07.02](steps/07-deployment-reconciliation/07.02-single-replica-reconcile-loop.md) | 07 | Single-replica reconcile loop | Not started |  |  |
| [07.03](steps/07-deployment-reconciliation/07.03-rolling-update.md) | 07 | Rolling update (start new → ready → shift → stop old) | Not started |  |  |
| [07.04](steps/07-deployment-reconciliation/07.04-unhealthy-rollout-automatic-rollback.md) | 07 | Unhealthy rollout → automatic rollback | Not started |  |  |
| [07.05](steps/07-deployment-reconciliation/07.05-deployment-history-and-restart-safety.md) | 07 | Deployment history + controller restart safety | Not started |  |  |
| [07.06](steps/07-deployment-reconciliation/07.06-demo-07-rolling-deployment.md) | 07 | Demo `07-rolling-deployment` + epic gate | Not started |  | Demo `07-rolling-deployment` gate |
| [08.01](steps/08-multi-node-scheduler/08.01-scheduler-skeleton-and-placement-apis.md) | 08 | Scheduler module/service skeleton + placement APIs | Not started |  |  |
| [08.02](steps/08-multi-node-scheduler/08.02-node-registration-heartbeat-resources.md) | 08 | Multi-node registration, heartbeat, resource reporting | Not started |  |  |
| [08.03](steps/08-multi-node-scheduler/08.03-first-fit-and-least-allocated-strategies.md) | 08 | First-fit and least-allocated placement strategies | Not started |  |  |
| [08.04](steps/08-multi-node-scheduler/08.04-anti-affinity-and-pending-queue.md) | 08 | Anti-affinity + pending queue | Not started |  |  |
| [08.05](steps/08-multi-node-scheduler/08.05-reschedule-on-node-offline.md) | 08 | Reschedule on node offline | Not started |  |  |
| [08.06](steps/08-multi-node-scheduler/08.06-demo-08-multi-node.md) | 08 | Demo `08-multi-node` + epic gate | Not started |  | Demo `08-multi-node` gate |
| [09.01](steps/09-forge-identity/09.01-skeleton-compose-postgres.md) | 09 | Skeleton + Compose + Postgres | Not started |  |  |
| [09.02](steps/09-forge-identity/09.02-users-orgs-memberships.md) | 09 | Users, orgs, memberships persistence | Not started |  |  |
| [09.03](steps/09-forge-identity/09.03-registration-login-sessions.md) | 09 | Registration, login, sessions | Not started |  |  |
| [09.04](steps/09-forge-identity/09.04-roles-and-project-membership.md) | 09 | Roles + project membership | Not started |  |  |
| [09.05](steps/09-forge-identity/09.05-api-tokens-service-accounts-revocation.md) | 09 | API tokens + service accounts + revocation | Not started |  |  |
| [09.06](steps/09-forge-identity/09.06-control-authz-middleware.md) | 09 | Control authz middleware (end `FORGE_AUTH_MODE=dev` default) | Not started |  |  |
| [09.07](steps/09-forge-identity/09.07-cli-login-and-token-profile.md) | 09 | CLI `forge login` + token profile | Not started |  |  |
| [09.08](steps/09-forge-identity/09.08-demo-09-platform-identity.md) | 09 | Demo `09-platform-identity` + epic gate | Not started |  | Demo `09-platform-identity` gate |
| [10.01](steps/10-forge-secrets/10.01-skeleton-and-encryption-key-bootstrap.md) | 10 | Skeleton + encryption key bootstrap | Not started |  |  |
| [10.02](steps/10-forge-secrets/10.02-encrypted-store-key-versioning-metadata.md) | 10 | Encrypted store + key versioning + metadata APIs | Not started |  |  |
| [10.03](steps/10-forge-secrets/10.03-config-vs-secrets-and-project-isolation.md) | 10 | Config vs secrets APIs; project isolation | Not started |  |  |
| [10.04](steps/10-forge-secrets/10.04-runtime-injection-at-deploy.md) | 10 | Runtime injection at deploy | Not started |  |  |
| [10.05](steps/10-forge-secrets/10.05-cli-secret-and-config.md) | 10 | CLI `forge secret` / `forge config` | Not started |  |  |
| [10.06](steps/10-forge-secrets/10.06-access-audit-and-log-masking.md) | 10 | Access audit + log masking | Not started |  |  |
| [10.07](steps/10-forge-secrets/10.07-demo-10-secrets.md) | 10 | Demo `10-secrets` + epic gate | Not started |  | Demo `10-secrets` gate |
| [11.01](steps/11-forge-events/11.01-skeleton-and-nats-wiring.md) | 11 | Skeleton + NATS wiring | Not started |  |  |
| [11.02](steps/11-forge-events/11.02-publish-subscribe-api.md) | 11 | Publish/subscribe API | Not started |  |  |
| [11.03](steps/11-forge-events/11.03-durable-consumers-ack-retry.md) | 11 | Durable consumers, ack, retry | Not started |  |  |
| [11.04](steps/11-forge-events/11.04-dlq-and-inspect-apis.md) | 11 | DLQ + inspect APIs | Not started |  |  |
| [11.05](steps/11-forge-events/11.05-event-json-schemas.md) | 11 | Event JSON Schemas | Not started |  |  |
| [11.06](steps/11-forge-events/11.06-idempotency-keys-and-consumer-identity.md) | 11 | Idempotency keys + consumer identity | Not started |  |  |
| [11.07](steps/11-forge-events/11.07-demo-11-event-driven.md) | 11 | Demo `11-event-driven` (Go producer → Elixir consumer) + gate | Not started |  | Demo `11-event-driven` gate |
| [12.01](steps/12-forge-observe/12.01-skeleton-and-correlation-api-design.md) | 12 | Skeleton + correlation API design | Not started |  |  |
| [12.02](steps/12-forge-observe/12.02-instrumentation-checklist.md) | 12 | Instrumentation checklist on Control/Runtime/Gateway/Build | Not started |  |  |
| [12.03](steps/12-forge-observe/12.03-grafana-dashboards.md) | 12 | Grafana dashboards (platform/service/deployment/runtime) | Not started |  |  |
| [12.04](steps/12-forge-observe/12.04-log-query-and-filter.md) | 12 | Log query/filter by project/deployment/request/trace ID | Not started |  |  |
| [12.05](steps/12-forge-observe/12.05-cli-logs-follow.md) | 12 | CLI `forge logs --follow` | Not started |  |  |
| [12.06](steps/12-forge-observe/12.06-basic-alert-rules.md) | 12 | Basic alert rules | Not started |  |  |
| [12.07](steps/12-forge-observe/12.07-demo-12-observability.md) | 12 | Demo `12-observability` (one distributed trace) + gate | Not started |  | Demo `12-observability` gate |
| [13.01](steps/13-forge-storage/13.01-skeleton-local-fs-backend.md) | 13 | Skeleton + local FS backend | Not started |  |  |
| [13.02](steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) | 13 | Buckets + metadata + project isolation | Not started |  |  |
| [13.03](steps/13-forge-storage/13.03-streamed-upload-download.md) | 13 | Streamed upload/download | Not started |  |  |
| [13.04](steps/13-forge-storage/13.04-sha256-range-requests.md) | 13 | SHA-256 + range requests | Not started |  |  |
| [13.05](steps/13-forge-storage/13.05-signed-tokens-expiry.md) | 13 | Signed tokens + expiry | Not started |  |  |
| [13.06](steps/13-forge-storage/13.06-quotas-delete-durability.md) | 13 | Quotas + delete + restart durability | Not started |  |  |
| [13.07](steps/13-forge-storage/13.07-demo-and-gate.md) | 13 | Demo `13-object-storage` + gate | Not started |  | Demo `13-object-storage` gate |
| [14.01](steps/14-forge-models/14.01-skeleton-compose.md) | 14 | Skeleton + Compose | Not started |  |  |
| [14.02](steps/14-forge-models/14.02-model-registry.md) | 14 | Model registry + `GET /v1/models` | Not started |  |  |
| [14.03](steps/14-forge-models/14.03-local-embeddings-adapter.md) | 14 | Local embeddings adapter | Not started |  |  |
| [14.04](steps/14-forge-models/14.04-generate-classify-summarize.md) | 14 | Generate/classify/summarize endpoints | Not started |  |  |
| [14.05](steps/14-forge-models/14.05-streaming-async-jobs.md) | 14 | Streaming + async jobs | Not started |  |  |
| [14.06](steps/14-forge-models/14.06-usage-metrics-openapi-cli.md) | 14 | Usage metrics + OpenAPI; optional CLI `forge model` | Not started |  |  |
| [14.07](steps/14-forge-models/14.07-demo-and-gate.md) | 14 | Demo `14-model-serving` + gate | Not started |  | Demo `14-model-serving` gate |
| [15.01](steps/15-forge-agents/15.01-skeleton.md) | 15 | Skeleton | Not started |  |  |
| [15.02](steps/15-forge-agents/15.02-agent-registry-yaml.md) | 15 | Agent registry + YAML definitions | Not started |  |  |
| [15.03](steps/15-forge-agents/15.03-tool-registry-permissions.md) | 15 | Tool registry + per-call permission checks | Not started |  |  |
| [15.04](steps/15-forge-agents/15.04-run-engine.md) | 15 | Run engine: max steps, timeouts, history | Not started |  |  |
| [15.05](steps/15-forge-agents/15.05-platform-tools.md) | 15 | Platform tools | Not started |  |  |
| [15.06](steps/15-forge-agents/15.06-human-approval.md) | 15 | Human approval for destructive tools | Not started |  |  |
| [15.07](steps/15-forge-agents/15.07-seed-agents-cli.md) | 15 | Seed agents + CLI `forge agent` | Not started |  |  |
| [15.08](steps/15-forge-agents/15.08-demo-and-gate.md) | 15 | Demo `15-agent-runtime` + gate | Not started |  | Demo `15-agent-runtime` gate |
| [16.01](steps/16-forge-workflows/16.01-skeleton-otp-health.md) | 16 | Skeleton OTP + health | Not started |  |  |
| [16.02](steps/16-forge-workflows/16.02-definitions-durable-state.md) | 16 | Definitions + durable run state | Not started |  |  |
| [16.03](steps/16-forge-workflows/16.03-step-primitives.md) | 16 | Step primitives | Not started |  |  |
| [16.04](steps/16-forge-workflows/16.04-event-triggers-agent-steps.md) | 16 | Event triggers + agent steps | Not started |  |  |
| [16.05](steps/16-forge-workflows/16.05-human-approval-restarts.md) | 16 | Human approval across restarts | Not started |  |  |
| [16.06](steps/16-forge-workflows/16.06-compensation-rollback.md) | 16 | Compensation/rollback via Control | Not started |  |  |
| [16.07](steps/16-forge-workflows/16.07-demo-and-gate.md) | 16 | Demo `16-agent-workflow` + gate | Not started |  | Demo `16-agent-workflow` gate |
| [17.01](steps/17-forge-memory/17.01-skeleton-persistence.md) | 17 | Skeleton + persistence | Not started |  |  |
| [17.02](steps/17-forge-memory/17.02-collections-vectors-metadata.md) | 17 | Collections + fixed-dim vectors + metadata | Not started |  |  |
| [17.03](steps/17-forge-memory/17.03-upsert-cosine-nn.md) | 17 | Upsert + cosine NN query | Not started |  |  |
| [17.04](steps/17-forge-memory/17.04-namespace-acl.md) | 17 | Namespace/ACL via Identity project scope | Not started |  |  |
| [17.05](steps/17-forge-memory/17.05-models-embed-agents-tool.md) | 17 | Models embed + Agents retrieval tool | Not started |  |  |
| [17.06](steps/17-forge-memory/17.06-demo-and-gate.md) | 17 | Demo `17-agent-memory` + gate | Not started |  | Demo `17-agent-memory` gate |
| [18.01](steps/18-managed-postgresql/18.01-control-apis-provisioner-skeleton.md) | 18 | Control APIs + provisioner skeleton | Not started |  |  |
| [18.02](steps/18-managed-postgresql/18.02-create-instance-db-credentials.md) | 18 | Create instance/database/credentials | Not started |  |  |
| [18.03](steps/18-managed-postgresql/18.03-attach-secrets-runtime-injection.md) | 18 | Attach + Secrets/Runtime URL injection | Not started |  |  |
| [18.04](steps/18-managed-postgresql/18.04-backup-restore.md) | 18 | Backup + restore | Not started |  |  |
| [18.05](steps/18-managed-postgresql/18.05-rotation-deletion-protection.md) | 18 | Credential rotation + deletion protection | Not started |  |  |
| [18.06](steps/18-managed-postgresql/18.06-cli-demo-and-gate.md) | 18 | CLI `forge database *` + demo + gate | Not started |  | Demo `18-managed-database` gate |
| [19.01](steps/19-full-platform-demo/19.01-polyglot-product-scaffold.md) | 19 | Polyglot sample product | Not started |  |  |
| [19.02](steps/19-full-platform-demo/19.02-deploy-path.md) | 19 | Deploy path: Build→Runtime→Gateway→Events | Not started |  |  |
| [19.03](steps/19-full-platform-demo/19.03-identity-secrets-observe-storage-db.md) | 19 | Identity, Secrets, Observe, Storage, managed DB | Not started |  |  |
| [19.04](steps/19-full-platform-demo/19.04-models-agents-memory.md) | 19 | Models + Agents + Memory for diagnosis | Not started |  |  |
| [19.05](steps/19-full-platform-demo/19.05-failure-injection-workflow.md) | 19 | Failure injection + Workflows approval/rollback | Not started |  |  |
| [19.06](steps/19-full-platform-demo/19.06-acceptance-suite-and-gate.md) | 19 | `demos/09-full-platform` acceptance suite + docs | Not started |  | North-star gate `demos/09-full-platform` |

> Total atomic steps: **132** (including `00.01`). Unfinished planned: **131**.

