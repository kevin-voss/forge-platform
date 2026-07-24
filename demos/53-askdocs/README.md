# Demo 53 — AskDocs

Epic **53** gate: document Q&A product with a Python API + chat SPA,
managed Postgres (`documents` / `chunks` / `messages`), Forge Storage uploads, an
ingest worker that chunks on `document.uploaded`, **Forge Models** embeddings
(`local-embed-small`, dim **384**), **Forge Memory** collection `askdocs-chunks`,
and a tool-using **Forge Agent** (`askdocs-answerer`) that produces grounded,
cited answers — or refuses when retrieval is empty/weak — verified end-to-end
via the platform E2E harness.

`make demo DEMO=53` (and `HEADLESS=1`) is the epic 53 acceptance gate. Product
browser E2E lives at `tests/e2e/projects/03-askdocs/spec.ts`.

## What it proves

1. Deploy AskDocs onto Forge (API + worker + web, managed DB, Storage, Events,
   Models, Memory, Agents).
2. Gateway hosts `app.` / `api.` / `worker.askdocs.localhost` return 200.
3. `POST /documents` stores the object in bucket `askdocs-corpus` and publishes
   `document.uploaded`.
4. Ingest worker chunks → embeds → Memory upsert → `status=ready`.
5. `POST /chat` retrieves via Models+Memory, invokes Agent `askdocs-answerer`
   (`memory.search` tool under `FORGE_AGENTS_TOOLS_MODE=fake` dry-run plan), and
   returns a grounded answer with citations for the planted handbook fact.
6. Out-of-corpus questions return *"not in the documents"* (refusal guardrail).
7. Chat history (including citations) persists across API container restart.
8. Playwright E2E (`tests/e2e/projects/03-askdocs`): headed + headless browser path
   for upload → `ready` → planted-fact answer with citation → out-of-corpus refusal →
   history on reload; platform.expect covers Models↔Memory dim contract, Memory top-k
   planted chunk, Agent `memory.search` run trace, and Observe evidence.
9. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Python API (`POST /documents`, `POST /query`, `POST /chat` grounded) |
| `api/answer.py` | Grounded answerer + refusal guardrail |
| `api/agents.py` | Forge Agents HTTP client |
| `api/retrieve.py` | Query-time embed + kNN + citation mapping |
| `agents/askdocs-answerer.yaml` | forge-agents definition (`memory.search`) |
| `agents/askdocs-answerer.resource.yaml` | Portable `kind: Agent` resource doc |
| `worker/` | Ingest worker (`document.uploaded` → chunk → embed → upsert) |
| `fixtures/company-handbook.txt` | Planted-fact handbook |
| `run.sh` | Deploy / teardown; persistence + ingest + grounded/refusal proofs |
| `seed.sh` | Idempotent welcome chat seed |
| `demo.json` | Harness contract (`id: 03-askdocs`, `spec` + `services` incl. agents/observe) |
| `docker-compose.yml` | Overlay: Agents mount + LocalProvisioner + Gateway hosts |
| `../../tests/e2e/projects/03-askdocs/` | Browser E2E spec + fixed handbook fixture |

## Commands

```bash
# Full lifecycle via orchestrator (preferred / epic gate)
make demo DEMO=53
make demo DEMO=53 HEADLESS=1

# Same product via PROJECTS filter (demo.json id prefix)
make test-platform-e2e PROJECTS=03
make test-platform-e2e HEADLESS=1 PROJECTS=03

# Manual product deploy only (leave running for curl / browser checks)
./demos/53-askdocs/run.sh
curl -fsS -H 'Host: api.askdocs.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.askdocs.localhost' -H 'content-type: application/json' \
  -d '{"text":"When is the office closed?"}' \
  http://127.0.0.1:4000/chat

# Unit tests
cd demos/53-askdocs/api && python3 -m unittest -v \
  test_chunking.py test_embeddings.py test_store.py test_answer.py

./demos/53-askdocs/seed.sh   # idempotent (requires .demo-state from run.sh)
./demos/53-askdocs/run.sh --down

# Browser E2E (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/03-askdocs
HEADLESS=1 npx playwright test projects/03-askdocs
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.askdocs.localhost`:

* `http://api.askdocs.localhost:4000/health/ready`
* `http://worker.askdocs.localhost:4000/health/ready`
* `http://app.askdocs.localhost:4000/`

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: askdocs-db }
  storage:  { type: object, bucket: askdocs-corpus }
# Platform HTTP:
#   FORGE_MODELS_URL → local-embed-small (dim 384, FORGE_MODELS_BACKEND=fake)
#   FORGE_MEMORY_URL  → collection askdocs-chunks
#   FORGE_AGENTS_URL  → askdocs-answerer (FORGE_AGENTS_TOOLS_MODE=fake)
```

## Platform findings (recorded, not patched)

Epic 53 surfaces non-blocker findings in
[`docs/demo-projects/PLATFORM_FINDINGS.md`](../../docs/demo-projects/PLATFORM_FINDINGS.md):

* `F-006` — no `retrieve` tool alias / Control-applied `kind: Agent` (uses
  `memory.search` + defs-dir mount)
* `F-007` — Observe lacks connected AskDocs → Models/Memory/Agents evidence

The orchestrator marks the product **degraded** and still exits 0 when blockers
are zero. Fake backends (`FORGE_MODELS_BACKEND=fake`,
`FORGE_AGENTS_TOOLS_MODE=fake`) keep answers deterministic.
