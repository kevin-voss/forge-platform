# Demo 52 — SnapNote

Epic **52** step `52.01`: a notes product with managed Postgres CRUD, deployed onto
Forge and reachable through the Gateway. Later steps add object storage, the
attachment worker queue, and worker autoscaling.

## What it proves (52.01)

1. Deploy SnapNote onto Forge (`forge build` + `forge apply` + managed DB).
2. Gateway hosts `app.snapnote.localhost` / `api.snapnote.localhost` return 200.
3. Notes CRUD persists in managed Postgres across an API container restart.
4. `attachments` schema stub exists (upload/processing lands in 52.02+).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, SQL-backed `/notes` CRUD, attachments list stub) |
| `migrations/` | Idempotent Postgres schema (`notes`, `attachments`) |
| `public/` | Minimal SPA for creating/listing notes |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); managed DB + persistence proof |
| `seed.sh` | Idempotent two starter notes |
| `demo.json` | Harness `DemoProject` contract (`id: 02-snapnote`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts |

## Commands

```bash
# Full lifecycle via orchestrator (scaffold smoke until 52.05/52.06)
make demo DEMO=52
make demo DEMO=52 HEADLESS=1

# Manual product deploy only (leave running for curl checks)
./demos/52-snapnote/run.sh
curl -fsS -H 'Host: api.snapnote.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.snapnote.localhost' -H 'content-type: application/json' \
  -d '{"title":"Trip photos","body":"Lake day"}' http://127.0.0.1:4000/notes
curl -fsS -H 'Host: api.snapnote.localhost' http://127.0.0.1:4000/notes
./demos/52-snapnote/seed.sh   # idempotent
./demos/52-snapnote/run.sh --down

# Browser smoke (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/02-snapnote

# API unit + repository tests (repo test starts a Postgres container when Docker is available)
cd demos/52-snapnote/api && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.snapnote.localhost`. Services are
named `api` and `app`, so the product is reachable at:

* `http://api.snapnote.localhost:4000/health/ready`
* `http://api.snapnote.localhost:4000/notes`
* `http://app.snapnote.localhost:4000/`

## Managed database

`forge.yaml` declares:

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: snapnote-db }
```

`run.sh` materializes that with `forge database create/attach`, waits for the
`Database` to be available, and confirms `DATABASE_URL` is injected into the API
container (in-memory secrets client while `FORGE_SECRETS_URL=disabled`). Migrations
run on API boot; `seed.sh` upserts two notes. Deploy also creates a note, restarts
the API container, and asserts the note still lists.

## Out of scope (later steps)

* Object storage / presigned upload (`52.02`)
* Events queue + worker + idempotent thumbnails (`52.03`)
* Worker autoscaling (`52.04`)
* Full headed browser E2E (`52.05`) and epic gate (`52.06`)
