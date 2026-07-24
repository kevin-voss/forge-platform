# Demo 53 — AskDocs

Epic **53** (through 53.02): document Q&A product with a Python API + chat SPA,
managed Postgres (`documents` / `chunks` / `messages`), Forge Storage uploads, and an
ingest worker that chunks on `document.uploaded`. Chat remains an **echo** stub until
Models/Memory/Agents land in later steps.

`make demo DEMO=53` runs the platform E2E lifecycle via `demo.json`. Grounded RAG +
browser E2E land in `53.03`–`53.06`.

## What it proves (53.02)

1. Deploy AskDocs onto Forge (API + worker + web, managed DB, Storage, Events).
2. Gateway hosts `app.` / `api.` / `worker.askdocs.localhost` return 200.
3. `POST /documents` stores the object in bucket `askdocs-corpus` and publishes
   `document.uploaded`.
4. Ingest worker fetches the object and writes deterministic `chunks` rows
   (document stays `ingesting` until embeddings in 53.03).
5. Chat echo + history persistence still work (53.01).
6. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Python API (`POST /documents`, `POST /chat` echo, messages/docs/chunks) |
| `worker/` | Ingest worker (`document.uploaded` → chunk Postgres) |
| `fixtures/company-handbook.txt` | Planted-fact handbook for ingest proof |
| `migrations/` | Idempotent Postgres schema (`documents`, `chunks`, `messages`) |
| `public/` | Chat SPA + document upload |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Project / Applications / Services / Deployments + DB/storage deps |
| `worker/worker.yaml` | Portable Worker resource doc |
| `api/forge.yaml`, `worker/forge.yaml`, `web.forge.yaml` | Build manifests |
| `run.sh` | Deploy (`up`) / teardown (`--down`); persist + ingest proofs |
| `seed.sh` | Idempotent welcome chat turn |
| `demo.json` | Harness `DemoProject` contract (`id: 03-askdocs`) |
| `docker-compose.yml` | Overlay: LocalProvisioner, Gateway hosts, Events `document` stream |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=53
make demo DEMO=53 HEADLESS=1

# Same product via PROJECTS filter (demo.json id prefix)
make test-platform-e2e PROJECTS=03
make test-platform-e2e HEADLESS=1 PROJECTS=03

# Manual product deploy only
./demos/53-askdocs/run.sh
curl -fsS -H 'Host: api.askdocs.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: worker.askdocs.localhost' http://127.0.0.1:4000/health/ready

# Unit tests
cd demos/53-askdocs/api && python3 -m unittest -v test_chunking.py test_store.py

./demos/53-askdocs/seed.sh
./demos/53-askdocs/run.sh --down
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.askdocs.localhost`. Services are
named `api`, `app`, and `worker`:

* `http://api.askdocs.localhost:4000/health/ready`
* `http://worker.askdocs.localhost:4000/health/ready`
* `http://app.askdocs.localhost:4000/`

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: askdocs-db }
  storage:  { type: object, bucket: askdocs-corpus }
  # worker also declares queue: { type: durable, name: askdocs-ingest }
```

Models, Memory, and Agents are wired in `53.03`–`53.04`.
