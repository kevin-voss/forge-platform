# Getting started

## Prerequisites

* Docker with Compose v2+
* Make
* curl
* bash

## First run

```bash
cp .env.example .env   # or: make setup
make dev
make status
make demo DEMO=00
```

## Hybrid mode

Start infrastructure only, then run a single service on the host in later steps:

```bash
make infra-up
cd services/<service>
make dev
```

## Ports

See `docs/operations/ports.md`.

## How delivery works

1. Product vision / coarse roadmap: `specs.md`
2. Plan an epic into many steps: `docs/implementation/PLAN_STEPS.md`
3. Implement one step: `docs/implementation/IMPLEMENT_STEP.md`
4. Track status: `docs/implementation/progress.md`

Epic 00 is complete (`00.01`). Next recommended planning target: `01-runtime-contract`.
