# Demo 55 — PulseBoard

Epic **55** product: a live metrics dashboard that proves autoscaling under load
and observability surfacing. Step **55.01** scaffolds the Go API + SPA and
deploys both to Ready on `board.pulseboard.localhost` / `api.pulseboard.localhost`.

Later steps add HTTP request-rate autoscaling (55.02), node autoscaling (55.03),
Observe surfacing (55.04), full browser E2E (55.05), and the epic gate (55.06).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, `/stats`, counter `/hit`) — stateless |
| `public/` | Live dashboard SPA (HTML/CSS/vanilla JS) |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`) |
| `demo.json` | Harness `DemoProject` contract |
| `docker-compose.yml` | Overlay: Control `auth=dev`, Gateway `{service}.pulseboard.localhost` |

## Commands

```bash
# Full lifecycle via orchestrator (preferred)
make demo DEMO=55
make demo DEMO=55 HEADLESS=1

# Manual product deploy only (leave running for curl checks)
./demos/55-pulseboard/run.sh
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/stats
curl -fsS -H 'Host: board.pulseboard.localhost' http://127.0.0.1:4000/
./demos/55-pulseboard/run.sh --down

# API unit tests
cd demos/55-pulseboard/api && go test ./...
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.pulseboard.localhost`. Services
are named `api` and `board`, so the product is reachable at:

* `http://api.pulseboard.localhost:4000/health/ready`
* `http://api.pulseboard.localhost:4000/stats`
* `http://board.pulseboard.localhost:4000/`

## Build note

`run.sh` calls `forge build` when that CLI subcommand exists; otherwise it
`docker build` + `docker push` both images from source into the local registry
(`localhost:5000`). Deploy always uses `forge apply -f` and waits until both
deployments are active (Ready). No database is provisioned — PulseBoard is
intentionally stateless.
