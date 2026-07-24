# Demo 53 — AskDocs

Epic **53** (through 53.05): document Q&A product with a Python API + chat SPA,
managed Postgres (`documents` / `chunks` / `messages`), Forge Storage uploads, an
ingest worker that chunks on `document.uploaded`, **Forge Models** embeddings
(`local-embed-small`, dim **384**), **Forge Memory** collection `askdocs-chunks`,
and a tool-using **Forge Agent** (`askdocs-answerer`) that produces grounded,
cited answers — or refuses when retrieval is empty/weak. Browser E2E proves
upload → ready → cited grounded answer, out-of-corpus refusal, and chat history
persistence.

`make demo DEMO=53` runs the platform E2E lifecycle via `demo.json`. Gate wiring
lands in `53.06`.

## What it proves (53.04–53.05)

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
| `demo.json` | Harness contract (`id: 03-askdocs`, `spec` + `services` incl. agents/observe) |
| `docker-compose.yml` | Overlay: Agents mount + LocalProvisioner + Gateway hosts |
| `../../tests/e2e/projects/03-askdocs/` | Browser E2E spec + fixed handbook fixture |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=53
make demo DEMO=53 HEADLESS=1

# Manual product deploy only
./demos/53-askdocs/run.sh
curl -fsS -H 'Host: api.askdocs.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.askdocs.localhost' -H 'content-type: application/json' \
  -d '{"text":"When is the office closed?"}' \
  http://127.0.0.1:4000/chat

# Unit tests
cd demos/53-askdocs/api && python3 -m unittest -v \
  test_chunking.py test_embeddings.py test_store.py test_answer.py

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

## Platform finding (53.04)

Product design names the tool `retrieve` bound to a Memory collection and a
Control-applied `kind: Agent` resource. Platform today exposes `memory.search`
(collection as an argument) and loads agent YAML from `FORGE_AGENTS_DEFS_DIR`
only — see [`PLATFORM_FINDINGS.md`](../../docs/demo-projects/PLATFORM_FINDINGS.md)
**F-006**. AskDocs mounts `askdocs-answerer.yaml` and uses a deterministic dry-run
plan; grounding uses product retrieval (lexical boost for fake embeddings).

Browser E2E is `53.05` (`tests/e2e/projects/03-askdocs/spec.ts`).
