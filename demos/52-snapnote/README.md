# Demo 52 — SnapNote

Epic **52** through `52.05`: notes with file attachments in Forge Storage, published to a
durable Events queue, processed by an idempotent background worker, with **queueDepth
autoscaling** that raises and lowers worker replicas within bounds (retry pressure blocks
unsafe scale-down). Browser E2E proves upload → async thumbnail and burst → scale up/down.

## What it proves (52.04–52.05)

1. Deploy SnapNote onto Forge (`forge build` + `forge apply` + managed DB + Storage + Events + Autoscaler).
2. Gateway hosts `app` / `api` / `worker.snapnote.localhost` return 200.
3. Notes CRUD persists in managed Postgres across an API container restart.
4. Attachments: presigned PUT → `POST …/complete` publishes `attachment.uploaded`
   (Idempotency-Key = `attachment_id`) to queue `snapnote-attachments`.
5. `snapnote-worker` consumes with ack + `POST /v1/processed`; thumbnail object appears;
   metadata reaches `status=ready`. Restart-safe (exactly-once side effects).
6. `ScalingPolicy` `{ type: queueDepth, queue: snapnote-attachments, targetPerReplica: 20 }`
   on Worker `snapnote-worker` (`minReplicas=1`, `maxReplicas=8`):
   * Burst uploads enqueue backlog → `desiredReplicas` rises within bounds.
   * `status.lastRecommendation.metricType=queueDepth`.
   * `retryRate` above target blocks scale-down (`RetryPressureBlocksScaleDown`).
   * After drain, replicas fall back to `minReplicas`.
7. Playwright E2E (`tests/e2e/projects/02-snapnote`): headed + headless browser path for
   create → attach → `processing…` → thumbnail; burst → workers indicator rises → all
   `ready` → scale-down; platform.expect covers restart-mid-burst idempotency, ScalingPolicy
   bounds/queueDepth, and thumbnail retrieval from Storage.

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (notes CRUD + attachment presign/complete/download/stream + events publish) |
| `worker/` | Go worker (durable consume → thumbnail → mark ready); `worker.yaml` Worker resource doc |
| `fixtures/scaling-policy.yaml` | ScalingPolicy resource doc (queueDepth + retryRate) |
| `scripts/burst.sh` | Burst enqueue helper (N attachments + metrics depth) |
| `scripts/test_queue_scaling.py` | Unit tests for queueDepth math + bounds + retry hold |
| `migrations/` | Idempotent Postgres schema (`notes`, `attachments`) |
| `public/` | SPA: notes, attach + thumbnail poll, workers indicator |
| `nginx.conf` | Static files + `/storage/` + `/autoscaler/` same-origin proxies |
| `forge.yaml` | Portable manifests + `dependencies.database|storage|queue` + worker Application |
| `run.sh` | Deploy / teardown; DB + storage + queue/worker + autoscaling proofs |
| `seed.sh` | Idempotent two starter notes |
| `demo.json` | Harness `DemoProject` contract (`id: 02-snapnote`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts, Events, Autoscaler + metrics |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=52
make demo DEMO=52 HEADLESS=1

# Manual product deploy only (leave running for curl / browser checks)
./demos/52-snapnote/run.sh
curl -fsS -H 'Host: api.snapnote.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: worker.snapnote.localhost' http://127.0.0.1:4000/health/ready

# Burst enqueue (product must already be up)
./demos/52-snapnote/scripts/burst.sh --count 40 --depth 80

# Unit tests
python3 demos/52-snapnote/scripts/test_queue_scaling.py
cd demos/52-snapnote/api && go test ./...
cd demos/52-snapnote/worker && go test ./...

./demos/52-snapnote/run.sh --down

# Browser E2E (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/02-snapnote
HEADLESS=1 npx playwright test projects/02-snapnote
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
`worker/worker.yaml`; Control hosts the Worker resource while Application/Service/Deployment
`snapnote-worker` runs the containers. Autoscaler patches Worker `desiredReplicas`;
`run.sh` syncs that onto the Deployment.

QueueDepth for the autoscaler is published via `demo52-metrics` (`/admin/metrics?queue=`)
because forge-events does not yet expose that admin surface (same approach as demo 24).

## Out of scope (later steps)

* Full headed browser E2E (`52.05`) and epic gate (`52.06`)
* Optional Infrastructure node pressure under capacity (PulseBoard / epic 55 owns the full path)
