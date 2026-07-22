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

## Local topology (Epic 00)

See [local-infrastructure.md](local-infrastructure.md) for the Compose substrate delivered by step `00.01`.

Platform control-plane services are added as later **epics**, each planned into multiple atomic **steps**.
