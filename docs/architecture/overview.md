# Architecture overview

Forge Platform is a self-hosted developer platform. Products remain independent and consume platform capabilities through HTTP APIs, events, environment variables, credentials, CLI commands, and optional SDKs.

## Runtime boundary

Every deployable service:

1. ships as an OCI image
2. listens on `PORT`
3. exposes liveness and readiness endpoints
4. logs to stdout/stderr as structured JSON
5. receives configuration from the environment
6. shuts down gracefully
7. publishes OpenTelemetry signals where supported

The formal, language-agnostic contract (HTTP paths, identity JSON, log fields, env vars, shutdown grace, and epic 01 decisions such as `PORT` over `FORGE_HTTP_PORT`) is in [runtime-contract.md](../contracts/runtime-contract.md), with OpenAPI and examples under `/contracts`.

## Local topology (Epic 00)

See [local-infrastructure.md](local-infrastructure.md) for the Compose substrate delivered by step `00.01`.

Platform control-plane services are added as later **epics**, each planned into multiple atomic **steps**.
