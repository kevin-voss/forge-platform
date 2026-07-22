# Epic 20: Declarative resource API

## Status

Planning

## Milestone

**M1 — Standalone cloud core** (epics 20–25). This epic is the foundation the rest of M1 is built on: it defines the resource envelope, kind registry, storage schema, and API conventions every later kind (Node, NodePool, Region, Database, Volume, …) registers into.

## Goal

Add a declarative, Kubernetes-style resource model to Forge Control: every resource — new or existing — is described by `apiVersion`/`kind`/`metadata`/`spec`/`status`, stored generically in one `control.resources` table, addressable through a uniform CRUD + watch API, with optimistic concurrency (`resourceVersion`), generation tracking, typed conditions, label selectors, ownership/finalizers, and an SSE watch stream. When this epic is done, `forge apply -f forge.yaml` creates or updates an `Application` resource, its generation increments on every spec edit, its controller (the existing epic-07 reconciler, unchanged) drives `status` toward `Ready`, and a watching client sees `ADDED`/`MODIFIED`/`DELETED` events as they happen — all without a new service, a new port, or a single existing endpoint changing shape. Proven by `demos/20-declarative-resources`.

## Why this epic exists

Epics 00–19 shipped a hierarchical, hand-rolled API per resource type (`/v1/projects/{id}/applications`, `/v1/services/{id}/deployments`, …) with UUID path identifiers and bespoke DTOs. That was the right amount of structure to ship a working platform fast, but epics 21–43 each need to introduce new resource types (service records, network policies, node pools, databases, volumes, secrets bundles, DNS zones, backups, …) and every one of them would otherwise repeat the same hand-rolled CRUD, concurrency, and status plumbing. Epic 20 extracts that plumbing once, generically, so that epics 21+ *register a kind* instead of *building an API*. It also gives the platform the two primitives standalone-cloud operation depends on: a stable way to express desired state in a portable manifest (`forge apply`), and a stable way for controllers and clients to observe convergence (`status`, conditions, watch) without polling every endpoint by hand.

## Relationship to shipped epics

* **Extends epic [`02-forge-control`](02-forge-control.md).** The generic resource module lives inside the same Kotlin/Ktor service on port `4001`, the same Postgres database and `control` schema, the same Flyway migration sequence, the same `ErrorEnvelope`/`ApiException` types, the same `Idempotency-Key` handling, and the same structured-log/OTEL conventions from `02.06`/`02.07`. No new service, no new port, no new error format.
* **Extends epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) without touching it.** The reconciler continues to read/write `control.deployments` and `control.reconcile_status` through the exact repositories and services shipped in `07.01`–`07.05` (`DeploymentStore`, `RepositoryDeploymentStore`, `ReconciliationController`). Epic 20 adds a companion envelope row (generation, `resourceVersion`, labels, conditions) alongside those rows — it never becomes the reconciler's source of truth and never changes the reconciler's SQL, its polling loop, or its restart-recovery path (`StartupRecovery`). Every step in this epic that touches deployment data (`20.03`, `20.06`, `20.07`) states explicitly how the reconciler is unaffected.
* **Reuses epic [`05-forge-gateway`](05-forge-gateway.md) `05.06` (WebSocket + SSE proxy) as-is.** The new `GET /v1/watch/{plural}` endpoint is a normal `text/event-stream` response; Gateway's existing content-type-based SSE detection proxies it without any Gateway code change.
* **Compatibility rule: additive companion envelope + new parallel API surface, never a rewrite.** The shipped endpoints (`POST /v1/projects/{id}/applications`, `POST /v1/services/{id}/deployments`, etc.), their UUID path parameters, their response DTOs, and their OpenAPI paths are byte-for-byte unchanged — verified by the existing `ReconcileOpenApiContractTest`-style contract tests continuing to pass untouched. `20.07` adds a *new*, additive set of paths (`/v1/projects/{project}/environments/{environment}/applications/{name}`, …) that expose the same underlying rows through the new envelope shape. Both surfaces write through the same service-layer methods so there is exactly one source of truth per fact.
* Later epics (`21`–`43`) depend on the `KindRegistry`, `ResourceRepository` interface, and envelope types this epic ships; none of them are implemented here.

## Primary code areas

* `services/forge-control/src/main/kotlin/forge/control/resource/` — envelope types, kind registry, generic repository/routes, watch, conditions, ownership (new module)
* `services/forge-control/src/main/resources/db/migration/` — `V20_01`–`V20_08` migrations
* `contracts/openapi/forge-control.openapi.yaml` — additive schemas/paths for the generic API and the unchanged legacy paths
* `tools/forge-cli/` — new verbs (`apply`, `get`, `describe`, `delete`, `wait`) added in `20.07`
* `demos/20-declarative-resources/` — epic gate demo

## Suggested language

Kotlin, as an additional module inside Forge Control (`forge.control.resource`), per the same rationale epics 07 and 08 used for the reconciler and scheduler modules: no new moving parts until size or independent scaling demands an extraction, and no change to the Kotlin + Ktor language choice `specs.md` §4 makes for Control.

## Spec references

* `docs/architecture/standalone-cloud.md` → §2 Declarative resource model (this epic is the reference implementation of that section; the normative envelope/API shape is fixed by the shared M1 brief and restated verbatim in `20.01`/`20.02`)
* `specs.md` → Step 02 (Forge Control: domain model, migrations, error envelope, idempotency) — extended, not replaced
* `specs.md` → Step 07 (Reconciliation and deployment controller) — the consumer this epic must not disturb
* `docs/implementation/MASTER_PLAN.md` → global step queue; epic 20 begins at `N = 132`, immediately after epic 19's `N = 131`

## Dependencies

* Epic [`02-forge-control`](02-forge-control.md) — complete; Postgres/Ktor/migration/error/idempotency foundation
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — complete; must keep passing its own demo (`make demo DEMO=07`) after every step in this epic
* Epic [`05-forge-gateway`](05-forge-gateway.md) — complete; SSE proxy (`05.06`) reused for watch, unmodified
* Soft dependency only: epic [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — no blocking requirement; epic 20 does not register any scheduling-related kind, it only ships the registry those future kinds will use

## Out of scope for this epic

* Node, NodePool, Region, InfrastructureProvider, Policy, Model kinds — the registry mechanism exists here; the kinds themselves are registered by the epics that own them (08, 21–26, 33, 38)
* Real multi-tenant `organization` enforcement — accepted and stored, defaulted to a single implicit org until epic 09 (Identity) wires real tenancy
* Publishing resource events onto NATS/`forge-events` — epic 20's `resource_events` table and SSE watch are Control-local; a bridge into epic 11 is a natural, separate follow-up
* Any new standalone service or port — everything ships inside `forge-control` on `4001`
* Scheduling-aware placement of `Application` workloads — still epic 07/08's job; epic 20 is API and storage only
* Deep CLI UX (interactive prompts, TUI) beyond the verbs listed in `20.07`

## Portability contract

This epic is pure control-plane API and Postgres storage — it provisions nothing and has zero provider awareness, so it behaves identically on local Docker, bare metal, Hetzner, AWS EC2, and Azure VM by construction, not by extra effort:

* The `Application` spec this epic defines (`image`, `resources.{cpu,memory}`, `scaling`, `dependencies`) contains no provider name, machine type, region id, IP address, disk type, managed-service name, or credential — enforced structurally: those fields simply do not exist in this schema. Provider-specific facts belong on `NodePool`/`InfrastructureProvider` resources owned by later epics (23, 26), never on `Application`.
* `control.resources`, `control.resource_events`, and the watch/CRUD API run against the same Postgres instance every other Control data already uses — no new datastore, no managed-service dependency, no cloud-only code path.
* The gate demo (`make demo DEMO=20`) runs entirely on local Docker Compose; there is no cloud-only variant of this epic's acceptance criteria.

## Success demo

```bash
make demo DEMO=20
```

```text
forge apply -f forge.yaml                         → creates Application invoice-api (generation=1)
GET .../applications/invoice-api                  → phase=Pending, observedGeneration=0
watch stream                                       → ADDED invoice-api
epic-07 reconciler converges replicas              → controller PUTs /status
watch stream                                       → MODIFIED invoice-api, phase=Progressing → Ready
GET .../applications/invoice-api                  → generation=1, observedGeneration=1, phase=Ready
forge apply -f forge.yaml (edited image)          → generation=2
watch stream                                       → MODIFIED, observedGeneration catches up to 2, phase=Ready
concurrent PUT with a stale resourceVersion        → 409 resource_version_conflict
forge delete application invoice-api (finalizer set) → phase=Terminating, blocked until finalizer cleared
finalizer cleared                                  → resource actually deleted; watch shows DELETED
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [20.01](../steps/20-declarative-resource-api/20.01-resource-envelope-and-registry.md) | 132 | Resource envelope, kind registry, storage schema | Not started | Envelope types, `KindRegistry`, `control.resources` table, ULID ids; no public HTTP yet |
| [20.02](../steps/20-declarative-resource-api/20.02-generic-crud-and-concurrency.md) | 133 | Generic CRUD endpoints + optimistic concurrency | Not started | CRUD for every registered kind; merge/JSON patch; `resourceVersion` 409s; idempotency reuse |
| [20.03](../steps/20-declarative-resource-api/20.03-generation-status-and-conditions.md) | 134 | Generation tracking, status subresource, conditions | Not started | `/status` subresource, condition merge, phase helper, spec/status write separation |
| [20.04](../steps/20-declarative-resource-api/20.04-labels-selectors-and-listing.md) | 135 | Labels, annotations, filtering, pagination | Not started | `labelSelector`, field filters, cursor pagination, list `resourceVersion` |
| [20.05](../steps/20-declarative-resource-api/20.05-watch-api-and-resource-events.md) | 136 | Watch API + resource events | Not started | SSE watch, replay buffer, `410 Gone`, durable `resource_events` + `GET /v1/events` |
| [20.06](../steps/20-declarative-resource-api/20.06-ownership-finalizers-and-deletion.md) | 137 | Owner references, finalizers, terminating deletion | Not started | Cascade rules, finalizer-blocked delete, stateful-kind delete confirmation |
| [20.07](../steps/20-declarative-resource-api/20.07-compat-facade-and-forge-apply.md) | 138 | Compatibility facade for shipped APIs + `forge apply` | Not started | Application/Service/Deployment as kinds; byte-compatible facade; new CLI verbs |
| [20.08](../steps/20-declarative-resource-api/20.08-demo-20-declarative-resources.md) | 139 | Demo `20-declarative-resources` + epic gate | Not started | End-to-end apply/watch/generation/conflict/finalizer proof; epic gate |

## Assumptions

* `control.resources` is additive: it is not the new source of truth for `Application`/`Service`/`Deployment` spec data. Their legacy tables stay authoritative; `20.07` adds a companion envelope row per legacy entity so the generic API, watch, and conditions machinery covers them too, kept in sync in the same transaction as the legacy write.
* New kinds (everything except the three grandfathered `20.07` kinds) get server-assigned, ULID-prefixed `TEXT` ids (`app_…`, `svc_…`, `dpl_…`, and so on for future kinds); the three grandfathered kinds keep their existing UUID as `metadata.id` on both the legacy and the new API surface — documented once, in `20.07`, rather than introducing dual identifiers.
* `resourceVersion` is a single Postgres sequence shared by every kind (`control.resource_version_seq`), not a per-resource counter — this is what makes a watch `since=` cursor meaningful across an entire collection, matching the Kubernetes/etcd convention the brief's model is drawn from.
* `organization` is accepted and stored on every resource now so the schema does not change shape later, but defaults to a single implicit value (`FORGE_RESOURCE_DEFAULT_ORGANIZATION`, default `default`) until epic 09 (Identity) supplies real tenancy — the same deferral pattern epics 02–08 already use for `FORGE_AUTH_MODE=dev`.
* Deletion is soft at the storage layer (`deleted_at`), never a hard row removal: `deletion_timestamp` marks "delete requested, finalizers pending" (phase `Terminating`, still readable); `deleted_at` marks the terminal state once finalizers are empty. This keeps the append-only-history ethos already used for `deployment_events`/`audit_log` and matches the exact column list the shared brief specifies for `20.01`.
* Auth remains `FORGE_AUTH_MODE=dev` throughout this epic, exactly as epics 02–08 do; `/status`-subresource write restriction to "the owning controller" is enforced by a documented `X-Forge-Controller` header convention now, replaced by real service identity in epic 09.

## Open questions

* **Does `Service` fit the two-level `project/environment/{plural}` shape?** Shipped `Service` rows are unique per `Application`, not per `Project`, so a flat project-scoped uniqueness check is wrong for it. Assumption: `KindDescriptor` gains an optional `parentKind`; `Service` sets `parentKind = "Application"` and is addressed as `/v1/projects/{project}/applications/{application}/services/{name}`, with uniqueness enforced at `(project, application, name)` instead of the generic scope index. Introduced as an unused field in `20.01`, put to use in `20.07`.
* **`Deployment` has no `name` column today.** Assumption: `20.07` adds one, backfilled from the owning service's name, unique per `(environment, name)`; two applications reusing the same service name in one project/environment is a known, documented edge case deferred past this epic (single-application-per-project holds for the epic-20 demo and every shipped demo so far).
* **Who may write `/status`?** No service-identity mechanism exists before epic 09. Assumption: a documented `X-Forge-Controller: <name>` header, checked against `KindDescriptor.owningController`, is a soft convention behind `FORGE_AUTH_MODE=dev` — not enforced as real security, called out as temporary in `20.03`.
* **Cascade delete policy default.** Assumption: deleting a resource with existing owned dependents is rejected (`409 owned_resources_exist`) unless the caller passes `?cascade=orphan` or `?cascade=foreground`, mirroring Kubernetes' explicit-propagation-policy default of "don't guess."
* **Replay buffer vs durable table for watch.** Assumption: an in-memory bounded ring buffer (fast path, powers `since=` within recent history, returns `410 Gone` beyond it) is rehydrated from the tail of the durable `resource_events` table on Control restart, so watch resume survives a Control restart without keeping unbounded history in memory.

## Next step to implement

**[20.01](../steps/20-declarative-resource-api/20.01-resource-envelope-and-registry.md) — Resource envelope, kind registry, storage schema** (`N = 132`; no prior step in this epic, unblocks `20.02`–`20.07`).
