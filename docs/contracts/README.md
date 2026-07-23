# Contracts documentation

Machine-readable contracts live in `/contracts`.

This folder holds human-oriented notes:

* how OpenAPI / Protobuf / events are versioned
* review checklist for new contracts
* examples of platform-wide conventions

## Runtime contract (epic 01)

Normative language-agnostic deployable contract:

* Human doc: [runtime-contract.md](runtime-contract.md)
* OpenAPI: [`contracts/openapi/runtime-contract.openapi.yaml`](../../contracts/openapi/runtime-contract.openapi.yaml)
* Log schema + examples: [`contracts/examples/`](../../contracts/examples/)

Covers listen port, health endpoints, identity JSON, structured logs, env configuration, and graceful shutdown.

## Forge Control API (epic 02)

The desired-state Control API contract is
[`contracts/openapi/forge-control.openapi.yaml`](../../contracts/openapi/forge-control.openapi.yaml).
It documents all `/v1` project, environment, application, service, and deployment
operations, canonical error responses, and optional `Idempotency-Key` create retries.

## Declarative resource API (epic 20, planned)

The standalone-cloud phase introduces one generic resource contract — `apiVersion`,
`kind`, `metadata`, `spec`, `status` — shared by every capability, with uniform CRUD,
list/filter, watch, status subresource, owner references, and finalizers. Conventions are
specified in [`docs/concepts/resource-model.md`](../concepts/resource-model.md); the
machine-readable contract is added to
[`contracts/openapi/forge-control.openapi.yaml`](../../contracts/openapi/forge-control.openapi.yaml)
during epic `20` **alongside** the existing `/v1` endpoints, which keep their current
contract and tests.

## Forge Build API (epic 06)

Build-job API and `forge.yaml` manifest:

* OpenAPI: [`contracts/openapi/forge-build.openapi.yaml`](../../contracts/openapi/forge-build.openapi.yaml)
  (`POST /v1/builds`, `GET /v1/builds/{buildId}`, `GET /v1/builds/{buildId}/logs`)
* `forge.yaml` schema: [`contracts/examples/forge.schema.json`](../../contracts/examples/forge.schema.json)
* Example manifest: [`contracts/examples/forge.yaml.example`](../../contracts/examples/forge.yaml.example)
* Build request/response fixtures under [`contracts/examples/`](../../contracts/examples/)

## Forge Identity authz matrix (epic 09)

Role model and `(action → roles)` permission matrix used by
`POST /v1/authz/check` / `GET /v1/authz/matrix`:

* Human doc + parity JSON: [authz-permission-matrix.md](authz-permission-matrix.md)
* OpenAPI: [`contracts/openapi/forge-identity.openapi.yaml`](../../contracts/openapi/forge-identity.openapi.yaml)

## Secret log masking + access audit (epic 10)

Convention for masking configured secret values in logs, plus the Secrets audit
query contract:

* Human doc: [secret-log-masking.md](secret-log-masking.md)
* OpenAPI: [`contracts/openapi/forge-secrets.openapi.yaml`](../../contracts/openapi/forge-secrets.openapi.yaml)
  (`GET /v1/projects/{project_id}/audit`, env-scoped variant, `AuditEvent` schema)

## Observability correlation (epic 12)

Cross-service trace/request correlation headers, resource attributes, and log
fields used by instrumentation and Forge Observe queries:

* Human doc: [observability-correlation.md](observability-correlation.md)
* Instrumentation checklist: [instrumentation-checklist.md](instrumentation-checklist.md)
* Go constants: `services/forge-observe/internal/correlation`
* OpenAPI (Observe skeleton): [`contracts/openapi/forge-observe.openapi.yaml`](../../contracts/openapi/forge-observe.openapi.yaml)
* Verification notes: [`docs/testing/instrumentation.md`](../testing/instrumentation.md)
