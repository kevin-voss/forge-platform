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
