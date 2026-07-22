# Epic 00: Repository foundation

## Status

Complete

## Goal

Provide a monorepo, Make-driven workflow, local Compose infrastructure, and a documentation system that can plan and track multi-step service delivery.

## Why this epic exists

Every later service needs shared conventions, ports, observability, data stores, and an implementation process before feature code lands.

## Primary code areas

* repository root (`Makefile`, `compose.yaml`, `.env.example`)
* `infrastructure/`
* `scripts/`
* `demos/00-foundation/`
* `docs/`
* `.github/workflows/`

## Suggested language

N/A (platform substrate)

## Spec references

* `specs.md` → Step 00: Repository foundation
* `specs.md` → sections 3, 5, 11, 12, 13

## Dependencies

None

## Out of scope for this epic

* platform services (`forge-control`, …)
* runtime-contract demo apps
* identity, secrets, agents, workflows

## Success demo

```bash
make setup
make demo DEMO=00
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [00.01](../steps/00-repository-foundation/00.01-initialize-foundation.md) | Initialize repository foundation | Complete | Single foundation slice |

## Open questions

None for this epic. Next: plan `01-runtime-contract` with `PLAN_STEPS.md`.
