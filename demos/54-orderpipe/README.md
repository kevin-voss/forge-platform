# Demo 54 — OrderPipe

Epic **54** (54.05): multi-service order product with storefront SPA,
`order-api` (Go), `fulfillment` (Python), `notify` (Elixir), managed Postgres,
**Forge Discovery** peer wiring, **NetworkPolicy** `orderpipe-mesh`,
**Forge Events** `order.*` choreography, and an **order-saga** with charge retry +
refund compensation (Workflow resource + in-process driver; see **F-008**).

`make demo DEMO=54` runs the platform E2E lifecycle via `demo.json`. Browser E2E
and the epic gate land in `54.06`–`54.07`.

## What it proves (54.05)

1. Deploy all four services onto Forge (`forge build` / docker build + `forge apply` + managed DB).
2. Gateway hosts `shop` / `api` / `fulfillment` / `notify.orderpipe.localhost` return 200.
3. Discovery registers Ready endpoints; DNS answers `*.local.orderpipe.svc.forge`.
4. Happy-path order: saga validate→charge (reads `PSP_API_KEY`) then events advance
   `fulfilled`→`notified`; `saga_events` audit trail is consistent.
5. Declined order (`declineCharge: true`): charge retries (3) then compensates → `refunded`
   with no fulfill/notify side effects (idempotent re-run safe).
6. `forge-workflows` lists `order-saga` (mounted YAML). Engine cannot execute `orderpipe.*`
   actions yet — recorded as **F-008**; demo drives the saga in `orderpipe-api`.
7. `PSP_API_KEY` provisioned into forge-secrets (metadata-only list) and injected into
   the API container for the charge step.
8. NetworkPolicy allow/deny proofs still hold.
9. Tear down product resources (unless `KEEP=1`).

## Layout

| Path | Role |
|---|---|
| `api/` | Go order-api (`POST /orders` + saga driver + `/saga/*` handlers) |
| `definitions/order-saga.yaml` | Workflow resource (loaded by forge-workflows) |
| `docs/order-saga.md` | Saga contract + F-008 workaround notes |
| `fulfillment/` | Python: consumes `order.charged` → emits `order.fulfilled` |
| `notify/` | Elixir: consumes `order.fulfilled` → emits `order.notified` |
| `docs/order-events.md` | Event subject / consumer map |
| `migrations/` | Idempotent Postgres schema (`orders.decline_charge`, `saga_events`, …) |
| `public/` | Minimal shop SPA |
| `forge.yaml` | Portable resources + peer/events/`PSP_API_KEY` env |
| `network-policy.yaml` | Portable `orderpipe-mesh` NetworkPolicy docs |
| `run.sh` | Deploy / teardown; Discovery + Events + saga + Secrets proofs |
| `demo.json` | Harness `DemoProject` (`services` += `workflows`, `secrets`) |
| `docker-compose.yml` | Overlay: Discovery + Network + Events + Workflows + Secrets |

## Commands

```bash
make demo DEMO=54
make demo DEMO=54 HEADLESS=1

cd demos/54-orderpipe/api && go test ./...
./demos/54-orderpipe/run.sh
./demos/54-orderpipe/run.sh --down
```

## Saga + events

| Stage | Owner |
|---|---|
| validate / charge (+ refund compensate) | order-api saga driver (`api/saga.go`) |
| fulfill / notify | forge-events consumers (fulfillment + notify services) |

Injectable failure: `POST /orders` with `"declineCharge": true` (or email containing
`+declined@`).

## Dependencies

```yaml
dependencies:
  database: { type: postgres, plan: standard, name: orderpipe-db }
```

Browser E2E lands in `54.06`.
