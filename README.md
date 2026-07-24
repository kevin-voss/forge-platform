# Forge Platform

Self-hosted developer platform for building, deploying, operating, observing, and connecting polyglot applications.

> Products remain independent from the platform.

## Quick start

```bash
make setup
make dev
make status
make demo DEMO=00
```

Stop everything:

```bash
make stop
```

## Repository layout

* `services/` — platform control-plane and data-plane services (added incrementally)
* `tools/` — CLI and developer tooling
* `packages/` — optional language SDKs
* `contracts/` — OpenAPI, Protobuf, and event schemas
* `infrastructure/` — local Compose dependency configs
* `demos/` — step demos and acceptance scenarios
* `docs/implementation/` — step tracker and agent prompt
* `specs.md` — full product and implementation specification

## Make interface

```bash
make setup
make dev
make stop
make restart
make status
make logs
make build
make test
make test-unit
make test-integration
make test-e2e
make test-platform-e2e          # verification north-star (demos 01–05 + coverage)
make test-platform-e2e HEADLESS=1
make test-infrastructure
make lint
make format
make clean
make reset
make demo DEMO=00
```

## Documentation

* [Docs index](docs/README.md)
* [Getting started](docs/development/getting-started.md)
* [Repository layout](docs/development/repository-layout.md)
* [Local infrastructure](docs/architecture/local-infrastructure.md)
* [Platform E2E runbook](docs/demo-projects/RUNBOOK.md) (verification track north-star gate)
* [Implementation system](docs/implementation/README.md) (epics → many steps)
* [Roadmap](docs/implementation/roadmap.md)
* [Progress](docs/implementation/progress.md)
* [Plan steps prompt](docs/implementation/PLAN_STEPS.md)
* [Implement step prompt](docs/implementation/IMPLEMENT_STEP.md)
* [Specification](specs.md)
