# Demo 53 — AskDocs

Epic **53** (through 53.03): document Q&A product with a Python API + chat SPA,
managed Postgres (`documents` / `chunks` / `messages`), Forge Storage uploads, an
ingest worker that chunks on `document.uploaded`, **Forge Models** embeddings
(`local-embed-small`, dim **384**), and **Forge Memory** collection
`askdocs-chunks` for kNN retrieval. Chat remains an **echo** stub until Agents
land in `53.04`.

`make demo DEMO=53` runs the platform E2E lifecycle via `demo.json`. Grounded
agent answers + browser E2E land in `53.04`–`53.06`.

## What it proves (53.03)

1. Deploy AskDocs onto Forge (API + worker + web, managed DB, Storage, Events,
   Models, Memory).
2. Gateway hosts `app.` / `api.` / `worker.askdocs.localhost` return 200.
3. `POST /documents` stores the object in bucket `askdocs-corpus` and publishes
   `document.uploaded`.
4. Ingest worker fetches the object, writes deterministic `chunks`, embeds each
   chunk via Models, upserts vectors into Memory, sets `memory_id`, and moves the
   document to `status=ready`.
5. `POST /query` embeds the question, runs Memory kNN (+ lexical boost for the
   fake embedder), and returns top-k chunks with citation mapping — including the
   planted handbook fact for *"When is the office closed?"*.
6. Embedding contract is pinned: model `local-embed-small`, dim `384`, cosine
   collection `askdocs-chunks`.
7. Chat echo + history persistence still work (53.01).
8. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Python API (`POST /documents`, `POST /query`, `POST /chat` echo) |
| `api/models.py` / `api/memory.py` | Forge Models + Memory HTTP clients |
| `api/embeddings.py` | Pinned dim/model/collection contract |
| `api/retrieve.py` | Query-time embed + kNN + citation mapping |
| `api/ingest_embed.py` | Worker embed → Memory upsert → ready |
| `worker/` | Ingest worker (`document.uploaded` → chunk → embed → upsert) |
| `fixtures/company-handbook.txt` | Planted-fact handbook for ingest/retrieval proof |
| `migrations/` | Idempotent Postgres schema (`documents`, `chunks`, `messages`) |
| `public/` | Chat SPA + document upload |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Project / Applications / Services / Deployments + DB/storage deps |
| `worker/worker.yaml` | Portable Worker resource doc |
| `api/forge.yaml`, `worker/forge.yaml`, `web.forge.yaml` | Build manifests |
| `run.sh` | Deploy (`up`) / teardown (`--down`); persist + ingest + retrieval proofs |
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
curl -fsS -H 'Host: api.askdocs.localhost' -H 'content-type: application/json' \
  -d '{"text":"When is the office closed?","topK":5}' \
  http://127.0.0.1:4000/query

# Unit tests
cd demos/53-askdocs/api && python3 -m unittest -v test_chunking.py test_embeddings.py test_store.py

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
  # worker also declares queue: { type: durable, name: askdocs-documents }
# Platform HTTP (env defaults in Dockerfiles):
#   FORGE_MODELS_URL → local-embed-small (dim 384, FORGE_MODELS_BACKEND=fake)
#   FORGE_MEMORY_URL  → collection askdocs-chunks
```

Agents grounded answering lands in `53.04`.
