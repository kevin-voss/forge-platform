# Demo 51 — TaskFlow

Epic **51** product: a small team task manager that proves the core Forge path
(build → apply → Gateway routes). Step **51.01** scaffolds the Go API + SPA and
deploys both to Ready on `app.taskflow.localhost` / `api.taskflow.localhost`.

Later steps add managed Postgres (51.02), Identity auth (51.03), secrets (51.04),
full browser E2E (51.05), and the epic gate (51.06).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, in-memory `/tasks` CRUD stub) |
| `public/` | Shared minimal SPA (HTML/CSS/vanilla JS) |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`) |
| `seed.sh` | No-op until 51.02 |
| `demo.json` | Harness `DemoProject` contract |
| `docker-compose.yml` | Overlay: Control `auth=dev`, Gateway `{service}.taskflow.localhost` |

## Commands

```bash
# Full lifecycle via orchestrator (preferred)
make demo DEMO=51
make demo DEMO=51 HEADLESS=1

# Manual product deploy only (leave running for curl checks)
./demos/51-taskflow/run.sh
curl -fsS -H 'Host: api.taskflow.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: app.taskflow.localhost' http://127.0.0.1:4000/
./demos/51-taskflow/run.sh --down

# API unit tests
cd demos/51-taskflow/api && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.taskflow.localhost`. Services are
named `api` and `app`, so the product is reachable at:

* `http://api.taskflow.localhost:4000/health/ready`
* `http://app.taskflow.localhost:4000/`

## Build note

`run.sh` calls `forge build` when that CLI subcommand exists; otherwise it
`docker build` + `docker push` both images from source into the local registry
(`localhost:5000`). Deploy always uses `forge apply -f` and waits until both
deployments are active (Ready).
