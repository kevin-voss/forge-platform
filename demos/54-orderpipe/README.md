# Demo 54 — OrderPipe

Epic **54** (54.04): multi-service order product with storefront SPA,
`order-api` (Go), `fulfillment` (Python), `notify` (Elixir), managed Postgres,
**Forge Discovery** peer wiring, **NetworkPolicy** `orderpipe-mesh`, and
**Forge Events** `order.*` choreography that advances status
`placed → validated → charged → fulfilled → notified`.

`make demo DEMO=54` runs the platform E2E lifecycle via `demo.json`. Workflow
saga retry/compensation and full browser E2E land in `54.05`–`54.07`.

## What it proves (54.04)

1. Deploy all four services onto Forge (`forge build` / docker build + `forge apply` + managed DB).
2. Gateway hosts `shop` / `api` / `fulfillment` / `notify.orderpipe.localhost` return 200.
3. Discovery registers Ready endpoints for `api` / `fulfillment` / `notify` under
   `orderpipe` / `local`; DNS answers `*.local.orderpipe.svc.forge`.
4. Place-order publishes `order.placed`; consumers advance status through
   `validated` / `charged` / `fulfilled` / `notified` via forge-events.
5. `saga_events` audit trail mirrors the happy-path steps (`place`…`notify`, `outcome=ok`).
6. NetworkPolicy `orderpipe-mesh` still allows `api→fulfillment` / `api→notify` and
   denies `fulfillment→notify` (observable via `POST /debug/denied-call`).
7. Order + saga trail survive an API container restart (Postgres durability).
8. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Go order-api (`POST /orders` → `order.placed` + status consumers) |
| `fulfillment/` | Python: consumes `order.charged` → emits `order.fulfilled`; NetworkPolicy probe |
| `notify/` | Elixir: consumes `order.fulfilled` → emits `order.notified` |
| `docs/order-events.md` | Event subject / consumer map |
| `migrations/` | Idempotent Postgres schema (`orders`, `saga_events`, …) |
| `public/` | Minimal shop SPA |
| `Dockerfile.web` + `nginx.conf` | Static nginx image on port `8080` |
| `forge.yaml` | Portable Project / Applications / Services / Deployments + DB + peer/events env |
| `network-policy.yaml` | Portable `orderpipe-mesh` NetworkPolicy docs (applied via forge-network) |
| `*/forge.yaml`, `web.forge.yaml` | Build manifests for `forge build` |
| `run.sh` | Deploy (`up`) / teardown (`--down`); Discovery + Events + NetworkPolicy proofs |
| `check-discovery.sh` | Contract: no hard-coded peer DNS; `*.svc.forge` required |
| `seed.sh` | Idempotent catalog (`mug`, `shirt`, `sticker`) |
| `demo.json` | Harness `DemoProject` (`id: 04-orderpipe`; `services` includes `events`) |
| `docker-compose.yml` | Overlay: Control LocalProvisioner, Gateway hosts, Discovery + Network + Events |

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
cd demos/54-orderpipe/fulfillment && python3 -m unittest -v
cd demos/54-orderpipe/notify && mix test
./demos/54-orderpipe/check-discovery.sh

# NetworkPolicy denied-pair probe (after deploy)
curl -sS -H 'Host: fulfillment.orderpipe.localhost' -H 'content-type: application/json' \
  -d '{"fromWorkload":"<fulfillment-deployment-id>","toWorkload":"<notify-deployment-id>"}' \
  http://127.0.0.1:4000/debug/denied-call

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

## Event choreography

Schemas: `contracts/events/order.*.schema.json` (stream family `order`).

| Subject | Producer | Consumer |
|---|---|---|
| `order.placed` | order-api | order-api (`orderpipe-validate`) |
| `order.validated` | order-api | order-api (`orderpipe-charge`) |
| `order.charged` | order-api | fulfillment (`orderpipe-fulfill`) |
| `order.fulfilled` | fulfillment | order-api + notify |
| `order.notified` | notify | order-api (`orderpipe-mark-notified`) |

See [`docs/order-events.md`](docs/order-events.md).

## Discovery peers

Product peer URLs (defaults baked into the API image; retained for NetworkPolicy
HTTP proofs and later workflow steps):

* `FULFILLMENT_URL=http://fulfillment.svc.forge:8080`
* `NOTIFY_URL=http://notify.svc.forge:8080`
* `FORGE_DISCOVERY_URL=http://host.docker.internal:4109`
* `FORGE_DISCOVERY_PROJECT=orderpipe` / `FORGE_DISCOVERY_ENVIRONMENT=local`
* `FORGE_EVENTS_URL=http://host.docker.internal:4105`

## NetworkPolicy (`orderpipe-mesh`)

Environment default `deny-all` under project `orderpipe` / `local`, plus:

| Policy | Target | Allow ingress from |
|---|---|---|
| `orderpipe-mesh` | `orderpipe-fulfillment` | service `api` :8080/tcp |
| `orderpipe-mesh-notify` | `orderpipe-notify` | service `api` :8080/tcp |

So `order-api→fulfillment` / `order-api→notify` are allowed; `fulfillment→notify` is
denied. Debug: `POST /debug/denied-call` on fulfillment records
`network.policy.denied` and bumps `forge_network_policy_denied_total`.

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: orderpipe-db }
```

Workflows (retry/compensation) land in `54.05`.
