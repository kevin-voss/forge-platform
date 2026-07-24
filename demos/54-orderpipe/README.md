# Demo 54 — OrderPipe

Epic **54** scaffold (54.01): multi-service order product with storefront SPA,
`order-api` (Go), `fulfillment` (Python), `notify` (Elixir), and
`orders` / `order_items` / `saga_events` (+ catalog) in managed Postgres.
Place-order is a **synchronous stub** until events/workflows land in later steps.

`make demo DEMO=54` runs the platform E2E lifecycle via `demo.json`. Discovery,
network policy, events, saga, and full browser E2E land in `54.02`–`54.07`.

## What it proves (54.01)

1. Deploy all four services onto Forge (`forge build` / docker build + `forge apply` + managed DB).
2. Gateway hosts `shop` / `api` / `fulfillment` / `notify.orderpipe.localhost` return 200.
3. Storefront loads; `POST /orders` creates a `placed` order row.
4. Order survives an API container restart (Postgres durability).
5. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Go order-api (`POST /orders` stub, `GET /catalog`, health) |
| `fulfillment/` | Python health + `/fulfill` stub |
| `notify/` | Elixir health + `/notify` stub |
| `migrations/` | Idempotent Postgres schema |
| `public/` | Minimal shop SPA |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB dependency |
| `*/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); place-order persistence proof |
| `seed.sh` | Idempotent catalog (`mug`, `shirt`, `sticker`) |
| `demo.json` | Harness `DemoProject` contract (`id: 04-orderpipe`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts |

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

# Unit tests
cd demos/54-orderpipe/api && go test ./...
cd demos/54-orderpipe/fulfillment && python3 -m unittest -v test_health.py
cd demos/54-orderpipe/notify && mix test

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

`fulfillment` / `notify` are product-internal peers (discovery/network policy in
`54.02`/`54.03`); gateway hosts exist so the harness can probe Ready.

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: orderpipe-db }
```

Workflows, Events, Discovery, and Network land in `54.02`–`54.05`.
