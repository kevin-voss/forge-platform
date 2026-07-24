# Demo 53 — AskDocs

Epic **53** scaffold (53.01): document Q&A product with a Python API + chat SPA,
`documents` / `chunks` / `messages` schema in managed Postgres, and a chat **echo**
stub until Models/Memory/Agents land in later steps.

`make demo DEMO=53` runs the platform E2E lifecycle via `demo.json`. Full grounded
RAG + browser E2E lands in `53.02`–`53.06`.

## What it proves (53.01)

1. Deploy AskDocs onto Forge (`forge build` / docker build + `forge apply` + managed DB).
2. Gateway hosts `app.askdocs.localhost` / `api.askdocs.localhost` return 200.
3. Chat UI loads; `POST /chat` persists user + echo assistant messages.
4. History survives an API container restart (Postgres durability).
5. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Python API (`POST /chat` echo stub, `GET /messages`, health) |
| `migrations/` | Idempotent Postgres schema (`documents`, `chunks`, `messages`) |
| `public/` | Minimal chat SPA |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); persistence proof |
| `seed.sh` | Idempotent welcome chat turn |
| `demo.json` | Harness `DemoProject` contract (`id: 03-askdocs`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts |

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

# Unit tests (spins a Postgres container unless ASKDOCS_TEST_DATABASE_URL is set)
cd demos/53-askdocs/api && python3 -m unittest -v test_store.py

./demos/53-askdocs/seed.sh
./demos/53-askdocs/run.sh --down
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.askdocs.localhost`. Services are
named `api` and `app`:

* `http://api.askdocs.localhost:4000/health/ready`
* `http://app.askdocs.localhost:4000/`

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: askdocs-db }
```

Storage, Models, Memory, and Agents are wired in `53.02`–`53.04`.
