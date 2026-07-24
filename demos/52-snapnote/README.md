# Demo 52 — SnapNote

Epic **52** step `52.02`: notes with file attachments stored in Forge Storage via
presigned PUT/GET. Later steps add the attachment worker queue and autoscaling.

## What it proves (52.02)

1. Deploy SnapNote onto Forge (`forge build` + `forge apply` + managed DB + Storage).
2. Gateway hosts `app.snapnote.localhost` / `api.snapnote.localhost` return 200.
3. Notes CRUD persists in managed Postgres across an API container restart.
4. Attachments: API issues presigned upload URLs; objects land in bucket
   `snapnote-attachments`; metadata rows stay `status=pending`; downloadable via
   signed GET or streamed API content.

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (notes CRUD + attachment presign/download/stream) |
| `api/storage.go` | Forge Storage client (bucket ensure, sign, get) |
| `migrations/` | Idempotent Postgres schema (`notes`, `attachments`) |
| `public/` | SPA: create notes + attach files (browser PUT to signed URL) |
| `nginx.conf` | Static files + `/storage/` same-origin proxy to forge-storage |
| `forge.yaml` | Portable manifests + `dependencies.database` + `dependencies.storage` |
| `run.sh` | Deploy / teardown; DB + storage proofs |
| `seed.sh` | Idempotent two starter notes |
| `demo.json` | Harness `DemoProject` contract (`id: 02-snapnote`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=52
make demo DEMO=52 HEADLESS=1

# Manual product deploy only (leave running for curl / browser checks)
./demos/52-snapnote/run.sh
curl -fsS -H 'Host: api.snapnote.localhost' http://127.0.0.1:4000/health/ready

# Presign → PUT → list (API)
NOTE_ID=... # from POST /notes
curl -fsS -H 'Host: api.snapnote.localhost' -H 'content-type: application/json' \
  -d '{"filename":"lake.jpg","contentType":"image/jpeg"}' \
  http://127.0.0.1:4000/notes/$NOTE_ID/attachments
# then PUT bytes to uploadUrl (or use the SPA Attach file button)

./demos/52-snapnote/run.sh --down

# Browser smoke (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/02-snapnote

# API unit + repository + storage fake round-trip
cd demos/52-snapnote/api && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.snapnote.localhost`. Services are
named `api` and `app`, so the product is reachable at:

* `http://api.snapnote.localhost:4000/health/ready`
* `http://api.snapnote.localhost:4000/notes`
* `http://app.snapnote.localhost:4000/`

Browser upload URLs use `http://app.snapnote.localhost:4000/storage/...` (nginx
proxies to host-published `forge-storage:4107`) so PUT stays same-origin.

## Dependencies

`forge.yaml` declares:

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: snapnote-db }
  storage:  { type: object, bucket: snapnote-attachments }
```

`run.sh` materializes the database with `forge database create/attach`, starts
`forge-storage`, ensures the bucket exists, and proves a presign → PUT → GET
round-trip. Storage client env is baked into the API image (`FORGE_STORAGE_*`).

## Out of scope (later steps)

* Events queue + worker + idempotent thumbnails (`52.03`) — status stays `pending`
* Worker autoscaling (`52.04`)
* Full headed browser E2E (`52.05`) and epic gate (`52.06`)
