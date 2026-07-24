# Demo 51 — TaskFlow

Epic **51** product: a small team task manager that proves the core Forge path
(build → apply → managed Postgres → Identity auth → Gateway routes). Step
**51.03** adds signup/login PAT issuance, Bearer introspect middleware, app-role
gating (`admin`/`member`), and deploy-time RBAC (viewer PAT → 403 / developer → 201).

Later steps add secrets (51.04), full browser E2E (51.05), and the epic gate (51.06).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/auth/*`, SQL-backed `/tasks`, admin `DELETE /projects/{id}`) |
| `migrations/` | Idempotent Postgres schema (`users`, `projects`, `tasks`, `app_settings`) |
| `public/` | Minimal SPA with login/signup + role-gated delete control |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); Identity bootstrap + RBAC proof |
| `seed.sh` | Idempotent Identity users + admin/member + shared project + tasks |
| `demo.json` | Harness `DemoProject` contract |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway `{service}.taskflow.localhost` |

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

# API unit + repository tests (repo test starts a Postgres container when Docker is available)
cd demos/51-taskflow/api && go test ./...
```

## Auth model

* Signup/login register credentials with **forge-identity**, mint a **PAT**, and
  optionally an app JWT (HMAC, plaintext `JWT_SIGNING_KEY` until 51.04).
* Protected routes send `Authorization: Bearer <PAT|JWT>`; middleware introspects
  the PAT via Identity and attaches the local app role (`admin`/`member`).
* `DELETE /projects/{id}` is **admin-only** (members receive 403; SPA hides the control).
* Deploy RBAC (platform): `run.sh` issues viewer/developer PATs for the Control
  project and proves viewer → 403 / developer → 201 on `POST …/deployments`.

Seed logins: `admin@taskflow.local` / `AdminPass123!` and
`member@taskflow.local` / `MemberPass123!`.

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
container (plaintext env until 51.04 moves it behind forge-secrets). Migrations
run on API boot; `seed.sh` upserts Identity-backed admin/member users, one shared
project, and two open tasks. Deploy also creates a task, restarts the API
container, and asserts the task still lists.
