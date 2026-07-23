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

## Platform services (selected)

| Service | Language | Host port | Notes |
|---|---|---:|---|
| Forge Storage | Rust (Axum) | 4107 | Local FS backend at `FORGE_STORAGE_ROOT`; object APIs in later epic-13 steps |
| Forge Models | Python (FastAPI) | 4300 | Model-serving skeleton (`FORGE_MODELS_BACKEND=fake\|local`); inference in later epic-14 steps |
| Forge Agents | Python (FastAPI) | 4301 | Agent runtime skeleton; registry/tools/run engine in later epic-15 steps |
| Forge Workflows | Elixir (OTP/Bandit) | 4302 | Durable YAML workflows + Postgres run/step state with resume-on-boot |

## Where this is heading

After epic `19`, Forge becomes a standalone cloud that runs identically on local Docker,
bare metal, Hetzner, AWS EC2, and Azure VMs, with providers supplying only primitives
(machines, disks, networks, IPs, GPUs). That target — declarative resources, service
discovery, its own overlay network, provider adapters, and unified autoscaling — is
specified in [standalone-cloud.md](standalone-cloud.md) and planned in
[`FUTURE_PLAN.md`](../implementation/FUTURE_PLAN.md). It changes nothing about the epics
above; it extends them.
