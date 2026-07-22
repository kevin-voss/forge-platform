# Epic 01: Runtime contract and demo applications

## Status

In progress

## Goal

Define and prove a language-agnostic container runtime contract—listen port, liveness/readiness, identity JSON, structured logs, env-based config, and graceful shutdown—using five minimal demo apps (Go, Kotlin, Rust, Python, Elixir) and one shared validator, with no platform SDK dependencies.

## Why this epic exists

Every later Forge service and customer product must share the same deployable boundary. Codifying that boundary before Control/Runtime/Gateway exist prevents five incompatible “almost contracts” and gives demos that later epics can schedule and route.

## Primary code areas

* `docs/contracts/` — human-readable runtime contract
* `contracts/openapi/`, `contracts/examples/` — machine-readable HTTP + log examples
* `tools/contract-validator/` — shared compliance runner
* `demos/01-container-runtime/` — Compose demo and per-language apps

## Suggested language

Polyglot demos: Go, Kotlin, Rust, Python, Elixir. Validator language is implementer choice (shell/Go/Python).

## Spec references

* `specs.md` → §2.2 Containers are the runtime boundary
* `specs.md` → §2.3 Share contracts, not implementations
* `specs.md` → §5.4–5.5 logging and configuration (subset for demos)
* `specs.md` → Step 01: Runtime contract and demo applications

## Dependencies

* Epic [`00-repository-foundation`](00-repository-foundation.md) complete (Make, Compose foundation, docs/contracts placeholders, port map, demo runner pattern)

No later epics are required.

## Out of scope for this epic

* Forge Control / CLI / Runtime / Gateway implementation
* Mandatory OpenTelemetry export from demo apps
* `/metrics` as a hard requirement (recommended only)
* Platform SDKs under `packages/*`
* Routing, scheduling, reconciliation, identity, secrets

## Success demo

```bash
make demo DEMO=01
```

Docker Compose starts five contract-compliant apps on ports `4201–4205`; the shared validator checks health, identity, logs, and graceful shutdown for each.

```text
Docker Compose
├── Go      :4201
├── Kotlin  :4202
├── Rust    :4203
├── Python  :4204
└── Elixir  :4205
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [01.01](../steps/01-runtime-contract/01.01-document-runtime-contract.md) | Document runtime contract | Complete | Docs + OpenAPI + log schema + port reservations |
| [01.02](../steps/01-runtime-contract/01.02-contract-test-runner.md) | Shared contract test runner | Complete | `tools/contract-validator`; fixture-tested |
| [01.03](../steps/01-runtime-contract/01.03-go-demo-app.md) | Go demo application | Complete | First vertical slice + demo 01 scaffold |
| [01.04](../steps/01-runtime-contract/01.04-python-demo-app.md) | Python demo application | Not started | Depends on 01.03; port 4204 |
| [01.05](../steps/01-runtime-contract/01.05-kotlin-demo-app.md) | Kotlin demo application | Not started | Depends on 01.03; port 4202 |
| [01.06](../steps/01-runtime-contract/01.06-rust-demo-app.md) | Rust demo application | Not started | Depends on 01.03; port 4203; matches spec example |
| [01.07](../steps/01-runtime-contract/01.07-elixir-demo-and-full-suite.md) | Elixir demo and full suite | Not started | Fifth language + epic acceptance gate |

## Assumptions

* Demo apps live under `demos/01-container-runtime/apps/<language>/` (not under `services/`).
* Host ports: Go `4201`, Kotlin `4202`, Rust `4203`, Python `4204`, Elixir `4205`.
* Normative listen env var for workloads is `PORT`; if `FORGE_HTTP_PORT` is also set, `PORT` wins (to be written in 01.01).
* OTEL and `/metrics` are documented as recommended, not required, for epic 01 demos.
* Identity endpoint is `GET /` (not `/info`), matching the simple JSON example in Step 01.

## Open questions

* Should CI run `make demo DEMO=01` on every PR, or only a subset (e.g. Go + validator) until image build times are acceptable?
* Preferred stacks per language (Ktor vs raw JDK; Axum vs Actix; Bandit vs Cowboy) — leave to implementers unless a repo standard appears?

## Decisions (from 01.01)

* Demo structured-log required fields: reduced subset `timestamp`, `level`, `service`, `message` (see `docs/contracts/runtime-contract.md`).
* Positive demos may keep ready≡live; negative “not ready” fixtures belong to the validator if needed.

## Next step to implement

**[01.04](../steps/01-runtime-contract/01.04-python-demo-app.md) — Python demo application** (depends on completed 01.03).
