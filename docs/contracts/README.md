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

## Forge Build API (epic 06)

Build-job API and `forge.yaml` manifest:

* OpenAPI: [`contracts/openapi/forge-build.openapi.yaml`](../../contracts/openapi/forge-build.openapi.yaml)
  (`POST /v1/builds`, `GET /v1/builds/{buildId}`, `GET /v1/builds/{buildId}/logs`)
* `forge.yaml` schema: [`contracts/examples/forge.schema.json`](../../contracts/examples/forge.schema.json)
* Example manifest: [`contracts/examples/forge.yaml.example`](../../contracts/examples/forge.yaml.example)
* Build request/response fixtures under [`contracts/examples/`](../../contracts/examples/)
