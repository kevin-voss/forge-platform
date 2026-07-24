# Demo 55 — PulseBoard

Epic **55** product: a live metrics dashboard that proves autoscaling under load
and observability surfacing. Step **55.02** adds HTTP request-rate autoscaling
(`ScalingPolicy httpRequests`) and a harness-driven load generator against
`api.pulseboard.localhost`.

Later steps add node autoscaling (55.03), Observe surfacing (55.04), full
browser E2E (55.05), and the epic gate (55.06).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, `/stats`, counter `/hit`) — stateless |
| `public/` | Live dashboard SPA (HTML/CSS/vanilla JS) |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `fixtures/scaling-policy.yaml` | ScalingPolicy resource doc (httpRequests, 1–10) |
| `scripts/loadgen.sh` | Start/stop load against `api.pulseboard.localhost` |
| `scripts/test_http_scaling.py` | Unit tests for RPS → replica math + fixture shape |
| `run.sh` | Deploy (`up`) / teardown (`--down`) + scale up/down proof |
| `demo.json` | Harness `DemoProject` contract (`services` includes autoscaler) |
| `docker-compose.yml` | Overlay: Control `auth=dev`, Gateway host pattern, Autoscaler + demo55-metrics |

## Commands

```bash
# Full lifecycle via orchestrator (preferred)
make demo DEMO=55
make demo DEMO=55 HEADLESS=1

# Manual product deploy (includes httpRequests scale-up/down proof)
./demos/55-pulseboard/run.sh
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/stats
curl -fsS -H 'Host: board.pulseboard.localhost' http://127.0.0.1:4000/

# Load generator (also used by run.sh / future E2E)
./demos/55-pulseboard/scripts/loadgen.sh start --rps 250
./demos/55-pulseboard/scripts/loadgen.sh status
./demos/55-pulseboard/scripts/loadgen.sh stop

./demos/55-pulseboard/run.sh --down

# Unit tests
cd demos/55-pulseboard/api && go test ./...
python3 demos/55-pulseboard/scripts/test_http_scaling.py
```

## Autoscaling (55.02)

* `ScalingPolicy` `{ type: httpRequests, targetValue: 50 }` on `pulseboard-api`
  with bounds `[1, 10]`.
* Load generator hits `api.pulseboard.localhost` and publishes matching RPS to
  `demo55-metrics` (Gateway does not yet expose `/admin/metrics`; same pattern
  as demos 24/52).
* `run.sh` asserts: start load → `desiredReplicas` climbs within bounds and
  policy status reflects RPS; stop load → replicas fall back to `minReplicas`.

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
