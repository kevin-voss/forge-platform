# Demo 55 — PulseBoard

Epic **55** gate: a live metrics dashboard that proves autoscaling under load and
observability surfacing — HTTP request-rate replicas, Docker NodePool capacity,
and Observe/Grafana parity — verified end-to-end via the platform E2E harness.

`make demo DEMO=55` (and `HEADLESS=1`) is the epic 55 acceptance gate. Product
browser E2E lives at `tests/e2e/projects/05-pulseboard/spec.ts`.

## What it proves

1. Deploy dashboard SPA + API onto Forge (`forge build` / docker build + `forge apply`).
2. Gateway hosts `board` / `api.pulseboard.localhost` return 200; `/stats` is Observe-sourced.
3. `ScalingPolicy` `{ type: httpRequests, targetValue: 50 }` grows `pulseboard-api` replicas
   within `[1, 10]` under load and drains back to `minReplicas` when load stops.
4. Operator `NodePool pulseboard-pool` (`minNodes: 2`, `maxNodes: 3`) provisions a Docker
   node when replicas exceed capacity, then drains back to `minNodes`.
5. Dashboard `/stats` replicas/RPS/p95 match Observe PromQL (and Prometheus/Grafana when ready).
6. Playwright E2E (`tests/e2e/projects/05-pulseboard`): headed + headless browser path for
   baseline → load → UI+Grafana replica climb → scale down. Soft platform asserts cover
   autoscaler httpRequests status, Control Application→Deployment actuation, and
   Observe/Grafana consistency. Opt-in NodePool browser leg:
   `PULSEBOARD_E2E_NODE_LEG=1` (default off — `run.sh` hard-proves scale-up/drain during
   deploy; see `F-009` for cooldown `0` semantics).
7. Tear down product resources + NodePool (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Go API (`/health/ready`, Observe-backed `/stats`, counter `/hit`) + OTEL emit |
| `public/` | Live dashboard SPA (HTML/CSS/vanilla JS) |
| `metrics/` | Observe metrics sidecar (`/api/v1/query`, `/metrics`, Gateway admin) |
| `prometheus/prometheus.yml` | Scrapes demo55-metrics for Grafana parity |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments |
| `api/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `fixtures/scaling-policy.yaml` | ScalingPolicy resource doc (httpRequests, 1–10) |
| `fixtures/nodepool-docker.yaml` | InfrastructureProvider + NodePool (`pulseboard-pool`, 2–3) |
| `scripts/loadgen.sh` | Start/stop load against `api.pulseboard.localhost` |
| `scripts/test_http_scaling.py` | Unit tests for RPS → replica math + fixture shape |
| `scripts/test_node_scaling.py` | Unit tests for slot → node math + NodePool fixture |
| `scripts/test_observe_surfacing.py` | Unit tests for Observe PromQL sidecar |
| `run.sh` | Deploy / teardown; HTTP + node scale + Observe proofs |
| `demo.json` | Harness contract (`id: 05-pulseboard`, `services` incl. autoscaler/infra/observe/apply) |
| `docker-compose.yml` | Overlay: autoscaler, infra, Observe metrics, Prometheus scrape |
| `../../tests/e2e/projects/05-pulseboard/` | Browser E2E spec |

## Commands

```bash
# Full lifecycle via orchestrator (preferred / epic gate)
make demo DEMO=55
make demo DEMO=55 HEADLESS=1

# Same product via PROJECTS filter (demo.json id prefix)
make test-platform-e2e PROJECTS=05
make test-platform-e2e HEADLESS=1 PROJECTS=05

# Manual product deploy only (leave running for curl / browser checks)
./demos/55-pulseboard/run.sh
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.pulseboard.localhost' http://127.0.0.1:4000/stats
curl -fsS -H 'Host: board.pulseboard.localhost' http://127.0.0.1:4000/

# Load generator (also used by run.sh / E2E)
./demos/55-pulseboard/scripts/loadgen.sh start --rps 250
./demos/55-pulseboard/scripts/loadgen.sh status
./demos/55-pulseboard/scripts/loadgen.sh stop

# Unit tests
cd demos/55-pulseboard/api && go test ./...
python3 demos/55-pulseboard/scripts/test_http_scaling.py
python3 demos/55-pulseboard/scripts/test_node_scaling.py
python3 demos/55-pulseboard/scripts/test_observe_surfacing.py

./demos/55-pulseboard/run.sh --down

# Browser E2E (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/05-pulseboard
HEADLESS=1 npx playwright test projects/05-pulseboard
# Opt-in capacity leg (Docker node add/drain; run.sh already proves this):
PULSEBOARD_E2E_NODE_LEG=1 npx playwright test projects/05-pulseboard
```

## Observe surfacing

* API emits OTEL traces/metrics when `FORGE_OTEL_ENABLED=true` and always queries
  `FORGE_OBSERVE_URL` for `/stats` (replicas / RPS / p95).
* Workload pods use `FORGE_OBSERVE_URL=http://host.docker.internal:4197` (host-
  published demo55-metrics port) because they sit on Docker bridge without
  compose DNS; platform services still reach `demo55-metrics:8080` in-net.
* `demo55-metrics` is the Prometheus-compatible Observe metrics backend
  (`/api/v1/query`, `/v1/metrics/query`, `/metrics`) — same pattern autoscaler
  uses when pointed at a PromQL endpoint.
* Control→Deployment sync publishes `replicas` into the sidecar; loadgen publishes
  RPS/p95; Prometheus scrapes `/metrics` for Grafana at `http://127.0.0.1:3000`.
* `run.sh` asserts dashboard `/stats` matches Observe (and Prometheus when ready)
  within tolerance.

## Autoscaling

* `ScalingPolicy` `{ type: httpRequests, targetValue: 50 }` on `pulseboard-api`
  with bounds `[1, 10]`.
* Operator `NodePool pulseboard-pool` `{ provider: docker, minNodes: 2, maxNodes: 3 }`
  (`docker-small` = 2 slots/node). Baseline api+web fits the min pool; load that
  wants 5 API replicas needs 6 slots → a third Docker node, then drain back to 2.
* Load generator hits `api.pulseboard.localhost` and publishes matching RPS to
  `demo55-metrics` (Gateway does not yet expose `/admin/metrics`; same pattern
  as demos 24/52).
* `run.sh` asserts: start load → `desiredReplicas` climbs and `readyNodes` rises
  within bounds after unschedulable demand; stop load → replicas fall to
  `minReplicas` and the idle node drains back to `minNodes`.

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

## Platform findings (recorded, not patched)

Epic 55 surfaces a non-blocker finding in
[`docs/demo-projects/PLATFORM_FINDINGS.md`](../../docs/demo-projects/PLATFORM_FINDINGS.md):

* `F-009` — forge-autoscaler treats node scale cooldown `<=0` as built-in defaults
  (60s up / 5m down), not “disabled”; PulseBoard sets cooldown to `1`s

Metric freshness, replica consistency, and first-cycle node drain stayed within gate
tolerances. The orchestrator exits 0 with zero blockers.
