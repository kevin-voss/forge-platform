# Demo 54 — OrderPipe

Epic **54** (54.02): multi-service order product with storefront SPA,
`order-api` (Go), `fulfillment` (Python), `notify` (Elixir), managed Postgres,
and **Forge Discovery** peer wiring. `order-api` calls peers as
`http://fulfillment.svc.forge:8080` / `http://notify.svc.forge:8080` (Ready
endpoint selection via Discovery), never compose DNS.

`make demo DEMO=54` runs the platform E2E lifecycle via `demo.json`. Network
policy, events, saga, and full browser E2E land in `54.03`–`54.07`.

## What it proves (54.02)

1. Deploy all four services onto Forge (`forge build` / docker build + `forge apply` + managed DB).
2. Gateway hosts `shop` / `api` / `fulfillment` / `notify.orderpipe.localhost` return 200.
3. Discovery registers Ready endpoints for `api` / `fulfillment` / `notify` under
   `orderpipe` / `local`; DNS answers `*.local.orderpipe.svc.forge`.
4. Place-order reaches fulfillment + notify via Discovery names; contract check
   (`check-discovery.sh`) rejects hard-coded peer DNS.
5. Order survives an API container restart (Postgres durability).
6. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Go order-api (`POST /orders` + Discovery peer client, catalog, health) |
| `fulfillment/` | Python health + `/fulfill` stub |
| `notify/` | Elixir health + `/notify` stub |
| `migrations/` | Idempotent Postgres schema |
| `public/` | Minimal shop SPA |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB + peer env |
| `*/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); Discovery + place-order proof |
| `check-discovery.sh` | Contract: no hard-coded peer DNS; `*.svc.forge` required |
| `seed.sh` | Idempotent catalog (`mug`, `shirt`, `sticker`) |
| `demo.json` | Harness `DemoProject` contract (`id: 04-orderpipe`; `services` includes `discovery`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts, Discovery defaults |

## Commands

```bash
# Full lifecycle via orchestrator
make demo DEMO=54
make demo DEMO=54 HEADLESS=1

# Same product via PROJECTS filter (demo dir prefix or id)
make test-platform-e2e PROJECTS=54
make test-platform-e2e HEADLESS=1 PROJECTS=04

# Manual product deploy only
./demos/54-orderpipe/run.sh
curl -fsS -H 'Host: shop.orderpipe.localhost' http://127.0.0.1:4000/

# Unit + contract checks
cd demos/54-orderpipe/api && go test ./...
cd demos/54-orderpipe/fulfillment && python3 -m unittest -v test_health.py
cd demos/54-orderpipe/notify && mix test
./demos/54-orderpipe/check-discovery.sh

./demos/54-orderpipe/seed.sh
./demos/54-orderpipe/run.sh --down
```

## Host routing

Gateway overlay sets `FORGE_HOST_PATTERN={service}.orderpipe.localhost`. Services are
named `shop`, `api`, `fulfillment`, and `notify`:

* `http://shop.orderpipe.localhost:4000/`
* `http://api.orderpipe.localhost:4000/health/ready`
* `http://fulfillment.orderpipe.localhost:4000/health/ready`
* `http://notify.orderpipe.localhost:4000/health/ready`

## Discovery peers

Product peer URLs (defaults baked into the API image):

* `FULFILLMENT_URL=http://fulfillment.svc.forge:8080`
* `NOTIFY_URL=http://notify.svc.forge:8080`
* `FORGE_DISCOVERY_URL=http://host.docker.internal:4109`
* `FORGE_DISCOVERY_PROJECT=orderpipe` / `FORGE_DISCOVERY_ENVIRONMENT=local`

`order-api` resolves Ready endpoints from Discovery, then POSTs `/fulfill` and
`/notify`. DNS FQDNs are `fulfillment.local.orderpipe.svc.forge` (and notify/api).

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: orderpipe-db }
```

Network policy, Events, and Workflows land in `54.03`–`54.05`.
