# Demo 50 — Harness self-test

Epic **50.07** gate: a trivial static “hello” product that proves the platform E2E
harness lifecycle end-to-end.

## What it proves

1. Deploy a product onto Forge (`forge apply` + Runtime + Gateway).
2. Open `http://hello.localhost:4000` in a real browser (headed or headless).
3. Click **Say hello** → assert **Hello, Forge**.
4. Deliberately record one `minor` sample finding via `platform.expect`, then clean
   it from `PLATFORM_FINDINGS.md` so the gate exits 0.
5. Tear down product resources.

## Layout

| Path | Role |
|---|---|
| `public/` | Shared SPA style (HTML/CSS/vanilla JS) |
| `Dockerfile` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Application / Service / Deployment |
| `run.sh` | Deploy (`up`) / teardown (`--down`) |
| `seed.sh` | No-op seed (idempotent) |
| `demo.json` | Harness `DemoProject` contract |
| `docker-compose.yml` | Overlay: Control `auth=dev`, Gateway `{service}.localhost` |

## Commands

```bash
# Full lifecycle via orchestrator (preferred)
make demo DEMO=50
make demo DEMO=50 HEADLESS=1

# Same entry via PROJECTS filter
make test-platform-e2e PROJECTS=50
make test-platform-e2e HEADLESS=1 PROJECTS=50

# Manual product deploy only
./demos/50-e2e-harness/run.sh
./demos/50-e2e-harness/run.sh --down
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.localhost`. The service is named
`hello`, so the product is reachable at `hello.localhost` on gateway port `4000`.
