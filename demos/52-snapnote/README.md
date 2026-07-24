# Demo 52 — SnapNote

Epic **52** step `52.03`: notes with file attachments in Forge Storage, published to a
durable Events queue, processed by an idempotent background worker that writes a
thumbnail and flips `status` to `ready`.

## What it proves (52.03)

1. Deploy SnapNote onto Forge (`forge build` + `forge apply` + managed DB + Storage + Events).
2. Gateway hosts `app` / `api` / `worker.snapnote.localhost` return 200.
3. Notes CRUD persists in managed Postgres across an API container restart.
4. Attachments: presigned PUT → `POST …/complete` publishes `attachment.uploaded`
   (Idempotency-Key = `attachment_id`) to queue `snapnote-attachments`.
5. `snapnote-worker` consumes with ack + `POST /v1/processed`; thumbnail object appears;
   metadata reaches `status=ready`. Restart-safe (exactly-once side effects).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (notes CRUD + attachment presign/complete/download/stream + events publish) |
| `worker/` | Go worker (durable consume → thumbnail → mark ready); `worker.yaml` Worker resource doc |
| `migrations/` | Idempotent Postgres schema (`notes`, `attachments`) |
| `public/` | SPA: create notes, attach files, poll until thumbnail ready |
| `nginx.conf` | Static files + `/storage/` same-origin proxy to forge-storage |
| `forge.yaml` | Portable manifests + `dependencies.database|storage|queue` + worker Application |
| `run.sh` | Deploy / teardown; DB + storage + queue/worker proofs |
| `seed.sh` | Idempotent two starter notes |
| `demo.json` | Harness `DemoProject` contract (`id: 02-snapnote`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts, Events `attachment` stream |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=52
make demo DEMO=52 HEADLESS=1

# Manual product deploy only (leave running for curl / browser checks)
./demos/52-snapnote/run.sh
curl -fsS -H 'Host: api.snapnote.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: worker.snapnote.localhost' http://127.0.0.1:4000/health/ready

# Presign → PUT → complete (API)
NOTE_ID=... # from POST /notes
ATT=$(curl -fsS -H 'Host: api.snapnote.localhost' -H 'content-type: application/json' \
  -d '{"filename":"lake.jpg","contentType":"image/jpeg"}' \
  http://127.0.0.1:4000/notes/$NOTE_ID/attachments)
# PUT bytes to uploadUrl, then:
curl -fsS -H 'Host: api.snapnote.localhost' -X POST \
  http://127.0.0.1:4000/notes/$NOTE_ID/attachments/$ATT_ID/complete
# poll GET …/attachments/$ATT_ID until status=ready

./demos/52-snapnote/run.sh --down

# Browser smoke (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/02-snapnote

# Unit tests
cd demos/52-snapnote/api && go test ./...
cd demos/52-snapnote/worker && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.snapnote.localhost`. Services are
named `api`, `app`, and `worker`:

* `http://api.snapnote.localhost:4000/health/ready`
* `http://worker.snapnote.localhost:4000/health/ready`
* `http://app.snapnote.localhost:4000/`

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: snapnote-db }
  storage:  { type: object, bucket: snapnote-attachments }
  queue:    { type: durable, name: snapnote-attachments }
```

Logical queue `snapnote-attachments` maps to forge-events subject `attachment.uploaded`
and durable consumer `snapnote-attachments`. Portable `kind: Worker` doc lives at
`worker/worker.yaml`; Control deploys the runnable form as Application/Service/Deployment
`snapnote-worker` until Worker is a first-class controller.

## Out of scope (later steps)

* Worker autoscaling (`52.04`)
* Full headed browser E2E (`52.05`) and epic gate (`52.06`)
