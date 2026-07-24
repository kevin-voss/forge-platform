# Demo 54 — OrderPipe

Epic **54** gate: multi-service order product with storefront SPA,
`order-api` (Go), `fulfillment` (Python), `notify` (Elixir), managed Postgres,
**Forge Discovery** peer wiring, **NetworkPolicy** `orderpipe-mesh`,
**Forge Events** `order.*` choreography, and an **order-saga** with charge retry +
refund compensation (Workflow resource + in-process driver; see **F-008**) —
verified end-to-end via the platform E2E harness.

`make demo DEMO=54` (and `HEADLESS=1`) is the epic 54 acceptance gate. Product
browser E2E lives at `tests/e2e/projects/04-orderpipe/spec.ts`.

## What it proves

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
8. NetworkPolicy allow/deny proofs still hold (denied fulfillment↔notify; allowed api→peers).
9. Playwright E2E (`tests/e2e/projects/04-orderpipe`): headed + headless browser path for
   happy order → `notified`, declined order → compensated `refunded`, and network-policy
   proof; soft platform asserts cover Discovery DNS, Events choreography, Workflows listing,
   and Secrets metadata.
10. Tear down product resources (unless `KEEP=1`).

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
| `seed.sh` | Idempotent catalog seed |
| `demo.json` | Harness contract (`id: 04-orderpipe`, `spec` + `services` incl. workflows/secrets) |
| `docker-compose.yml` | Overlay: Discovery + Network + Events + Workflows + Secrets |
| `../../tests/e2e/projects/04-orderpipe/` | Browser E2E spec |

## Commands

```bash
# Full lifecycle via orchestrator (preferred / epic gate)
make demo DEMO=54
make demo DEMO=54 HEADLESS=1

# Same product via PROJECTS filter (demo.json id prefix)
make test-platform-e2e PROJECTS=04
make test-platform-e2e HEADLESS=1 PROJECTS=04

# Manual product deploy only (leave running for curl / browser checks)
./demos/54-orderpipe/run.sh
curl -fsS -H 'Host: api.orderpipe.localhost' http://127.0.0.1:4000/health/ready
curl -fsS -H 'Host: api.orderpipe.localhost' -H 'content-type: application/json' \
  -d '{"email":"buyer@example.com","sku":"mug","qty":1}' \
  http://127.0.0.1:4000/orders

# Unit tests
cd demos/54-orderpipe/api && go test ./...

./demos/54-orderpipe/seed.sh   # idempotent (requires .demo-state from run.sh)
./demos/54-orderpipe/run.sh --down

# Browser E2E (product must already be up via run.sh or KEEP=1)
cd tests/e2e && npx playwright test projects/04-orderpipe
HEADLESS=1 npx playwright test projects/04-orderpipe
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
# Platform HTTP:
#   FORGE_DISCOVERY_URL / FORGE_NETWORK_DNS_SEARCH → *.svc.forge peers
#   FORGE_EVENTS_URL → order.* subjects
#   FORGE_WORKFLOWS_URL → order-saga definition (list-only; F-008)
#   FORGE_SECRETS_URL → PSP_API_KEY metadata
```

## Platform findings (recorded, not patched)

Epic 54 surfaces a non-blocker finding in
[`docs/demo-projects/PLATFORM_FINDINGS.md`](../../docs/demo-projects/PLATFORM_FINDINGS.md):

* `F-008` — forge-workflows has no HTTP/service actions for product saga steps
  (OrderPipe drives validate/charge/refund in-process; Workflow YAML is mounted
  for listing only)

The orchestrator marks the product **degraded** and still exits 0 when blockers
are zero.
