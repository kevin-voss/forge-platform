# Epic 02: Forge Control

## Status

In progress

## Goal

Deliver the central control-plane API that is the single source of truth for the platform's desired state: projects, environments, applications, services, and desired deployments, backed by PostgreSQL with automatic migrations, stable UUID identifiers, a shared error format, an OpenAPI contract, and structured logs plus OpenTelemetry. When this epic is done, a developer (or the CLI in epic 03) can create a full project hierarchy and a desired deployment record and read the whole tree back, with state surviving a service restart. No container execution happens yet — Control only records intent.

## Why this epic exists

Every later platform capability — CLI (03), Runtime (04), Gateway (05), Build (06), reconciliation (07), scheduling (08) — reads desired state from or writes actual state to a single authoritative API. Building Control first establishes the domain model, ID scheme, error format, and idempotency conventions that all later services depend on. Without a stable control plane there is nothing for the CLI to drive, nothing for Runtime to reconcile against, and no place for Gateway to read service endpoints.

## Primary code areas

* `services/forge-control/` — Kotlin + Ktor service, domain model, repositories, HTTP API
* `contracts/openapi/forge-control.openapi.yaml` — machine-readable API contract
* `demos/02-control-plane/` — end-to-end hierarchy demo

## Suggested language

Kotlin + Ktor (per `specs.md` §4 language matrix). Ktor server with kotlinx.serialization, a JDBC/HikariCP + Flyway (or equivalent) migration setup against PostgreSQL. Implementers may choose the SQL layer (Exposed, jOOQ, or plain JDBC) as long as migrations are explicit and versioned.

## Spec references

* `specs.md` → Step 02: Forge Control
* `specs.md` → §4 Language matrix (Kotlin + Ktor for Control)
* `specs.md` → §5.4 structured logging, §5.5 configuration
* `specs.md` → §2.3 Share contracts, not implementations (OpenAPI first)
* Epic [`01-runtime-contract`](01-runtime-contract.md) → health, structured-log, and configuration conventions reused by every service

## Dependencies

* Epic [`00-repository-foundation`](00-repository-foundation.md) complete — Make interface, Docker Compose stack, PostgreSQL on host `5001`, OTEL Collector, service template, ports doc.
* Epic [`01-runtime-contract`](01-runtime-contract.md) conventions — health endpoints (`/health/live`, `/health/ready`), structured JSON logs, env-based configuration, graceful shutdown. Control is a platform service, but it reuses the same operational contract.

No later epics are required.

## Out of scope for this epic

* Container execution, image pulls, or workload lifecycle (epic 04)
* Reconciliation between desired and actual state (epic 07)
* Multi-node scheduling / placement (epic 08)
* Authentication and authorization enforcement (epic 09) — Control ships with a documented `FORGE_AUTH_MODE=dev` bypass until Identity `09.06`
* CLI (epic 03) — Control is exercised via `curl`/HTTP in this epic
* Gateway routing (epic 05)
* Secrets storage/injection (epic 10)

## Success demo

```bash
make demo DEMO=02
```

`demos/02-control-plane` starts Control (host `4001`) and PostgreSQL, then creates a project → development environment → application → service → desired deployment, and reads the complete hierarchy back. It asserts stable UUIDs, the shared error format on an invalid relationship, and state survival across a restart.

```text
POST /v1/projects
  └── POST /v1/projects/{id}/environments
        └── POST /v1/projects/{id}/applications
              └── POST /v1/applications/{id}/services
                    └── POST /v1/services/{id}/deployments
GET /v1/projects/{id}?expand=tree   → full hierarchy
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [02.01](../steps/02-forge-control/02.01-service-skeleton.md) | Service skeleton, health, Compose | Complete | Ktor module, Makefile, Dockerfile, port 4001 |
| [02.02](../steps/02-forge-control/02.02-domain-model-and-migrations.md) | Domain model + Postgres migrations | Complete | Schema `control`, Flyway, Hikari, JDBC repos |
| [02.03](../steps/02-forge-control/02.03-projects-environments-api.md) | Projects & environments API | Complete | First curlable slice; provisional error envelope |
| [02.04](../steps/02-forge-control/02.04-applications-services-api.md) | Applications & services API + relationship validation | Complete | Nested application/service APIs, relationship validation, and audit rows |
| [02.05](../steps/02-forge-control/02.05-deployments-desired-state-api.md) | Deployments desired-state API + basic audit | Not started | Depends on 02.04 |
| [02.06](../steps/02-forge-control/02.06-errors-openapi-contract-idempotency.md) | Shared errors, OpenAPI, contract tests, idempotency | Not started | Depends on 02.03–02.05 |
| [02.07](../steps/02-forge-control/02.07-structured-logs-and-otel.md) | Structured logs + OTEL | Not started | Depends on 02.01+ |
| [02.08](../steps/02-forge-control/02.08-demo-control-plane-and-gate.md) | Demo `02-control-plane` + epic gate | Not started | Depends on all prior |

## Assumptions

* Control service source lives under `services/forge-control/`; demo under `demos/02-control-plane/`.
* Host port `4001` (public range) maps to the in-container `PORT` (default `8080`).
* PostgreSQL is the shared foundation instance on host `5001`; Control owns schema `control` (or database `forge_control`) — decided in `02.02`.
* All resource identifiers are server-generated UUIDs (v4) returned as strings.
* API version prefix is `/v1`.
* Migrations run automatically on startup and are also invokable via a documented `make` target.
* The shared error envelope follows the platform convention introduced here and reused by later services.
* Until Identity `09.06`, Control runs with `FORGE_AUTH_MODE=dev` (all requests treated as an implicit dev principal); no auth is enforced.

## Open questions

* **Auth bypass:** Demos `02`–`08` rely on `FORGE_AUTH_MODE=dev`. Confirm the exact bypass semantics (implicit principal, header override) so `09.06` can remove it cleanly.
* **SQL layer:** Exposed vs jOOQ vs plain JDBC — leave to implementer, or set a repo standard here?
* **Environment cardinality:** Is `Environment` a child of `Project` only, or can an `Application`/`Service` be pinned to an environment? (Assumption: environments belong to a project; deployments reference an environment.)
* **Soft delete vs hard delete:** Do we need soft-delete/audit retention now, or is a simple audit table sufficient for the epic? (Assumption: append-only audit table, hard delete of resources deferred.)
* **Idempotency scope:** Which creates require `Idempotency-Key` support — all creates or only deployments? (Assumption: all `POST` creates accept an optional key; deployments strongly recommended.)

## Next step to implement

**[02.05](../steps/02-forge-control/02.05-deployments-desired-state-api.md) — Deployments desired-state API + basic audit** (depends on 02.04).
