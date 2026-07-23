# Demo 09 — Full platform (capstone)

Polyglot incident-management product deployed through the real Forge path, with
platform foundations, AI diagnosis, and the approval-gated recovery loop:

```text
Identity (enforce) → Control authz (viewer denied / developer deploy)
Secrets            → APP_SHARED_SECRET + PRODUCT_MODE + DATABASE_URL injection
Observe/OTEL       → distributed trace across api + admin + classify
Storage            → project bucket artifact via incident-api
Managed Postgres   → forge database create/attach → incidents table
Models + Memory    → historical incidents embedded; NN retrieval
Agents             → deployment-investigator (telemetry + memory.search)
Workflows          → incident-response (failure → diagnose → approve → rollback)
```

This folder is the **thematic** north-star demo (`demos/09-full-platform`). It is
**not** the Identity epic demo (`demos/09-platform-identity`).

## Status (epic 19)

| Step | Scope |
|---|---|
| **19.01** | Product services under `product/` (complete) |
| **19.02** | Deploy path Build→Runtime→Gateway→Events (complete) |
| **19.03** | Identity / Secrets / Observe / Storage / managed DB (complete) |
| **19.04** | Models / Agents / Memory diagnosis (complete) |
| **19.05** | Failure injection + Workflows approval/rollback (**this README**) |
| 19.06 | Acceptance suite packaging |

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

Authenticated product calls need `Authorization: Bearer <developer PAT>`.

| Host | Service |
|---|---|
| `api.demo.localhost` | incident-api |
| `admin.demo.localhost` | incident-admin |
| `logs.demo.localhost` | incident-log-worker |
| `classify.demo.localhost` | incident-classify |
| `notify.demo.localhost` | incident-notify |

## Deploy + foundations + AI diagnosis

```bash
cd demos/09-full-platform
./deploy.sh
```

What `deploy.sh` does:

1. Starts platform services (Identity, Secrets, Control LocalProvisioner, Runtime,
   Gateway, Build, Events, Observe, Storage, Models, Memory, Agents, OTEL/Tempo)
2. Registers owner/org; creates Control project; issues developer + viewer PATs
3. Asserts viewer cannot deploy; developer can
4. Runs [`setup-foundations.sh`](setup-foundations.sh): Secrets bindings,
   `forge database create/attach`, Storage bucket
5. Builds + deploys all five product services via **`forge deployment create`**
6. Asserts: DB status, secret status (no plaintext), Storage artifact round-trip,
   Tempo distributed trace across ≥3 product services, log masking
7. Seeds Memory + runs the investigator diagnosis loop (19.04)

## Failure injection + recovery (19.05)

Broken release flag (any product service):

```text
CAPSTONE_BREAK=true   # /health/ready → 503 {status:not_ready, error:capstone_break}
```

North-star recovery loop (CI subset with fake Control/Agents):

```bash
./scenario/break-release.sh
```

What it proves:

1. `CAPSTONE_BREAK` fails readiness deterministically (unit-tested)
2. Readiness failure → `deployment.failed` → `incident-response` workflow starts
3. Parallel diagnostics + Memory-assisted investigator diagnosis
4. Human approval required; mid-run workflows restart resumes without repeating steps
5. On **approve** → Control rollback → report stored → `deployment.completed` completion event
6. On **deny** → no rollback (hold/escalate via `on_deny: close`)

Manual (stack already up with workflows defs mounted):

```bash
./scenario/break-release.sh accept
# observe awaiting_approval, then:
# curl -X POST "$FORGE_WORKFLOWS_URL/v1/approvals/<id>/approve" \
#   -H "X-Forge-Project: capstone" -H 'content-type: application/json' \
#   -d '{"decided_by":"operator","reason":"rollback"}'
```

AI-only acceptance (Models/Memory/Agents, fake tools — CI path):

```bash
./ai/verify-diagnosis.sh
```

Unit tests:

```bash
python3 -m unittest discover -s lib -p 'test_*.py' -v
cd product/api-go && go test ./...
./scenario/break-release.sh unit
```

## AI layer (19.04)

| Path | Purpose |
|---|---|
| [`ai/fixtures/historical-incidents.json`](ai/fixtures/historical-incidents.json) | Seed corpus for Memory (Models embeddings) |
| [`ai/deployment-investigator.yaml`](ai/deployment-investigator.yaml) | Capstone agent: telemetry tools + `memory.search` + approval-gated `runtime.restart` |
| [`ai/seed-memory.sh`](ai/seed-memory.sh) | Create collection + upsert incidents |
| [`ai/verify-diagnosis.sh`](ai/verify-diagnosis.sh) | NN + investigator citation acceptance |

## Scenario layer (19.05)

| Path | Purpose |
|---|---|
| [`scenario/incident-response.yaml`](scenario/incident-response.yaml) | Capstone workflow: event → diagnose → approve → rollback → report |
| [`scenario/expected-report.md`](scenario/expected-report.md) | Final report shape |
| [`scenario/break-release.sh`](scenario/break-release.sh) | Failure injection + approval/rollback acceptance |

Configuration:

| Variable | Default | Purpose |
|---|---|---|
| `CAPSTONE_BREAK` | unset/`false` | Product readiness failure (broken v2) |
| `FORGE_MODELS_BACKEND` | `fake` | Deterministic embeddings in CI |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | Deterministic tool fixtures in CI |
| `FORGE_WORKFLOWS_AGENTS_MODE` | `fake` | Workflow agent client (fake in CI) |
| `FORGE_WORKFLOWS_CONTROL_MODE` | `fake` | Workflow Control rollback (fake in CI) |
| `FORGE_WORKFLOWS_URL` | `http://127.0.0.1:4302` | Workflows API |
| `FORGE_AGENTS_URL` | `http://127.0.0.1:4301` | Agents API |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control (live rollback) |
| `FORGE_EVENTS_URL` | `http://127.0.0.1:4105` | Events (completion publish when up) |

## Product endpoints (api-go)

| Path | Notes |
|---|---|
| `/db-status` | `DATABASE_URL_present` + backend; never echoes URL |
| `/secret-status` | `APP_SHARED_SECRET_present` + length; never echoes secret |
| `/incidents` | Persisted in managed Postgres when `DATABASE_URL` injected |
| `/artifacts` | Upload/download via Forge Storage (project-scoped) |

## Contracts used

Documented platform APIs only: Identity auth/tokens, Secrets set/bindings,
Control hierarchy + deployments + managed DB + reconcile/rollback, Runtime
injection, Gateway proxy, Events publish/consume (`deployment.failed` /
`deployment.completed`), Observe/Tempo traces, Storage buckets/objects, Models
embed, Memory upsert/query, Agents runs/tools/approvals, Workflows runs/approvals/
triggers. CLI:
`forge login|project|env|app|service|deployment|secret|config|database|agent`.
