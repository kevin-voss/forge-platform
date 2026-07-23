# Demo 09 — Full platform (capstone)

North-star gate for the Forge Platform. One command starts the demo; one command
runs the Step 19 acceptance suite proving:

```text
deploy → run → broken release → detect → workflow → agent diagnosis
  (telemetry + memory) → human approval → rollback → report → healthy again
```

This folder is the **thematic** north-star demo (`demos/09-full-platform`). It is
**not** the Identity epic demo (`demos/09-platform-identity`).

## Status (epic 19)

| Step | Scope |
|---|---|
| 19.01–19.05 | Product, deploy path, foundations, AI, failure/rollback |
| **19.06** | Acceptance suite + docs (**complete** — this gate) |

## Quick start

```bash
# From repo root
make demo DEMO=09-full-platform
make demo-accept DEMO=09-full-platform

# Alias
make demo-full
make demo-accept DEMO=09-full-platform

# From this directory
./start.sh
./accept.sh
echo "exit=$?"   # 0 on success
```

| Script | Purpose |
|---|---|
| [`start.sh`](start.sh) | Bring the demo to healthy |
| [`accept.sh`](accept.sh) | Run the full Step 19 test list |
| [`runbook.md`](runbook.md) | Operations + failure dumps |
| [`scenario/walkthrough.md`](scenario/walkthrough.md) | Narrative recovery walkthrough |

## CI subset vs full stack

| Variable | Default | Meaning |
|---|---|---|
| `CI_SUBSET` | `true` | Documented CI gate (Models/Agents/Memory/Workflows + fake backends) |
| `CI_SUBSET=false` | — | Full platform + product via `deploy.sh` (kept running) |
| `FORGE_MODELS_BACKEND` | `fake` | Deterministic embeddings |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | Deterministic tool fixtures |
| `FORGE_ACCEPT_KEEP` | `0` | Keep stack after accept when `1` |

Full local proof:

```bash
CI_SUBSET=false ./start.sh
CI_SUBSET=false ./accept.sh
```

## Acceptance tests (`tests/`)

| Script | Specs.md Step 19 item |
|---|---|
| `01-smoke.sh` | complete platform smoke test |
| `02-deploy.sh` | full deployment test |
| `03-identity.sh` | identity and permission test |
| `04-secrets.sh` | secret-injection test |
| `05-events.sh` | event-processing test |
| `06-telemetry.sh` | telemetry test |
| `07-models.sh` | model-serving test |
| `08-agents.sh` | agent-tool test |
| `09-workflow-recovery.sh` | workflow recovery test |
| `10-rollback.sh` | rollback test |
| `11-interop.sh` | multi-language interoperability test |
| `12-contracts.sh` | contract-only communication verification |

## Auth

`FORGE_AUTH_MODE=enforce` for Control + Secrets. Product API requires a Bearer
PAT (Identity introspect). Role difference:

* **developer** PAT → `forge deployment create` succeeds
* **viewer** PAT → Control deploy returns **403 forbidden**

## Gateway hostnames

See [`routes.md`](routes.md). Quick check (health stays open):

```bash
curl -fsS -H 'Host: api.demo.localhost' http://127.0.0.1:4000/health/ready
```

| Host | Service |
|---|---|
| `api.demo.localhost` | incident-api |
| `admin.demo.localhost` | incident-admin |
| `logs.demo.localhost` | incident-log-worker |
| `classify.demo.localhost` | incident-classify |
| `notify.demo.localhost` | incident-notify |

## Building blocks

| Path | Purpose |
|---|---|
| [`deploy.sh`](deploy.sh) | Full platform + product deploy (19.02–19.04) |
| [`setup-foundations.sh`](setup-foundations.sh) | Identity / Secrets / DB / Storage |
| [`ai/`](ai/) | Memory seed + investigator diagnosis |
| [`scenario/`](scenario/) | `CAPSTONE_BREAK` + incident-response workflow |
| [`product/`](product/) | Go / Kotlin / Rust / Python / Elixir services |

Broken release flag:

```text
CAPSTONE_BREAK=true   # /health/ready → 503 {status:not_ready, error:capstone_break}
```

## Contracts used

Documented platform APIs only: Identity, Secrets, Control, Runtime, Gateway, Build,
Events, Observe, Storage, Models, Agents, Memory, Workflows, managed Postgres.
`tests/12-contracts.sh` lints OpenAPI + event schemas and asserts product code
does not hard-code peer Docker DNS.
