# Demo 51 — TaskFlow

Epic **51** product: a small team task manager that proves the core Forge path
(build → apply → managed Postgres → Gateway routes). Step **51.02** adds a typed
`dependencies.database` managed Postgres, schema migrations, SQL-backed `/tasks`,
and idempotent `seed.sh`.

Later steps add Identity auth (51.03), secrets (51.04), full browser E2E (51.05),
and the epic gate (51.06).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, SQL-backed `/tasks` CRUD) |
| `migrations/` | Idempotent Postgres schema (`users`, `projects`, `tasks`) |
| `public/` | Shared minimal SPA (HTML/CSS/vanilla JS) |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); provisions managed DB + persist check |
| `seed.sh` | Idempotent admin/member + shared project + two open tasks |
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
curl -fsS -H 'Host: api.taskflow.localhost' http://127.0.0.1:4000/tasks
./demos/51-taskflow/seed.sh   # idempotent
./demos/51-taskflow/run.sh --down

# API unit + repository tests (repo test starts a Postgres container when Docker is available)
cd demos/51-taskflow/api && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.taskflow.localhost`. Services are
named `api` and `app`, so the product is reachable at:

* `http://api.taskflow.localhost:4000/health/ready`
* `http://api.taskflow.localhost:4000/tasks`
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
run on API boot; `seed.sh` upserts admin/member users, one shared project, and
two open tasks. Deploy also creates a task, restarts the API container, and
asserts the task still lists.
