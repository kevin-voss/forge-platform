# Demo 51 — TaskFlow

Epic **51** product: a small team task manager that proves the core Forge path
(build → apply → managed Postgres → Identity auth → Secrets injection → Gateway
routes). Step **51.04** moves `DATABASE_URL` and `JWT_SIGNING_KEY` into
forge-secrets (bindings + managed-db attach) with no plaintext in manifests or
logs.

Browser E2E lives at `tests/e2e/projects/01-taskflow/spec.ts` (step **51.05**).
Epic gate / `make demo DEMO=51` wiring is **51.06**.

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/auth/*`, SQL-backed `/tasks`, admin `DELETE /projects/{id}`) |
| `migrations/` | Idempotent Postgres schema (`users`, `projects`, `tasks`, `app_settings`) |
| `public/` | Minimal SPA with login/signup + role-gated delete control |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency + secret refs |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); Secrets + Identity + RBAC proof |
| `seed.sh` | Idempotent Identity users + admin/member + shared project + tasks |
| `demo.json` | Harness `DemoProject` contract |
| `docker-compose.yml` | Overlay: Secrets master key, Control LocalProvisioner, Gateway hosts |

## Commands

```bash
# Full lifecycle via orchestrator (preferred)
make demo DEMO=51
make demo DEMO=51 HEADLESS=1

# Manual product deploy only (leave running for curl checks)
./demos/51-taskflow/run.sh
curl -fsS -H 'Host: api.taskflow.localhost' http://127.0.0.1:4000/health/ready
# Login then list tasks
TOKEN=$(curl -fsS -H 'Host: api.taskflow.localhost' -H 'content-type: application/json' \
  -d '{"email":"admin@taskflow.local","password":"AdminPass123!"}' \
  http://127.0.0.1:4000/auth/login | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
curl -fsS -H 'Host: api.taskflow.localhost' -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:4000/tasks
./demos/51-taskflow/seed.sh   # idempotent
./demos/51-taskflow/run.sh --down

# Browser E2E (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/01-taskflow
HEADLESS=1 npx playwright test projects/01-taskflow

# API unit + repository tests (repo test starts a Postgres container when Docker is available)
cd demos/51-taskflow/api && go test ./...
```

## Auth model

* Signup/login register credentials with **forge-identity**, mint a **PAT**, and
  optionally an app JWT (HMAC; signing key injected from Forge Secrets).
* Protected routes send `Authorization: Bearer <PAT|JWT>`; middleware introspects
  the PAT via Identity and attaches the local app role (`admin`/`member`).
* `DELETE /projects/{id}` is **admin-only** (members receive 403; SPA hides the control).
* Deploy RBAC (platform): `run.sh` issues viewer/developer PATs for the Control
  project and proves viewer → 403 / developer → 201 on `POST …/deployments`.

Seed logins: `admin@taskflow.local` / `AdminPass123!` and
`member@taskflow.local` / `MemberPass123!`.

## Secrets (51.04)

Product design names `taskflow/db-url` / `taskflow/jwt-key` are illustrative;
forge-secrets requires `[A-Za-z_][A-Za-z0-9_]*`. `run.sh` materialises:

| Env var | Source |
|---|---|
| `DATABASE_URL` | `forge database attach` → Secrets `secretRef` (managed-db env) |
| `JWT_SIGNING_KEY` | `forge secret set` + bindings on service `api` |

`forge.yaml` documents `valueFrom.secret` refs (no plaintext). Boot fails clearly if
either env var is missing. `run.sh` greps the rendered manifest and platform/API
logs to assert zero secret leakage.

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.taskflow.localhost`. Services are
named `api` and `app`, so the product is reachable at:

* `http://api.taskflow.localhost:4000/health/ready`
* `http://api.taskflow.localhost:4000/auth/login`
* `http://api.taskflow.localhost:4000/tasks` (Bearer required)
* `http://app.taskflow.localhost:4000/`

## Managed database

`forge.yaml` declares:

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: taskflow-db }
```

`run.sh` materializes that with `forge database create/attach`, waits for the
`Database` to be available, and confirms `DATABASE_URL` is injected into the API
container from Forge Secrets. Migrations run on API boot; `seed.sh` upserts
Identity-backed admin/member users, one shared project, and two open tasks.
Deploy also creates a task, restarts the API container, and asserts the task
still lists.
