# Demo 4 — OrderPipe

**Epic:** [`54-demo-orderpipe`](../../implementation/epics/54-demo-orderpipe.md) · **Focus:**
multi-service **workflows** (a saga), **events** choreography, **service discovery**, and
**network policy** — the "several services talking to each other" story.

A small **order-processing pipeline**. A storefront places an order; the platform runs it through
`validate → charge (mock) → fulfill → notify`. If a step fails, the **workflow retries and
compensates** (refund/cancel). Multiple services in different languages find each other via
**Discovery** and are constrained by **NetworkPolicy**.

---

## 1. Why this product

Real systems are many services with a business process spanning them. OrderPipe proves the
platform's coordination layer: workflows drive a multi-step saga with recovery, events carry state
between services, discovery replaces hard-coded DNS, and network policy enforces who may talk to
whom. It's the multi-service counterpart to TaskFlow's single-service baseline.

## 2. Services exercised

| Service | How OrderPipe uses it | Proven by |
|---|---|---|
| forge-workflows | Order saga definition: validate→charge→fulfill→notify with retry + compensation. | Injected failure triggers retry then compensation. |
| forge-events | Services emit/consume `order.*` events (placed, validated, charged, fulfilled, notified). | Status advances via events. |
| forge-discovery | `order-api`, `fulfillment`, `notify` resolve each other by `*.svc.forge` name, not container DNS. | Contract test: no hard-coded peer DNS. |
| forge-network | Overlay + `NetworkPolicy`: only allowed pairs communicate; a denied pair is blocked. | Denied call fails; allowed call succeeds. |
| forge-secrets | Mock payment API key injected into the charge step. | No plaintext key; charge step reads it. |
| managed Postgres | Order + line-item state. | Final order state consistent with saga. |
| gateway/build/control/runtime/observe | Multi-service routing + telemetry. | Storefront reachable; saga trace. |

## 3. Architecture

```text
Browser ──▶ Gateway :4000  shop.orderpipe.localhost ─▶ storefront-web
                           api.orderpipe.localhost  ─▶ order-api (Go)

order-api ── place ─▶ forge-workflows (OrderSaga)
   Saga steps (each emits/consumes order.* via forge-events):
     validate  → order-api           (stock/format)
     charge    → order-api           (mock PSP, key from forge-secrets)   ← failure injected here
     fulfill   → fulfillment (Python)  discovered via fulfillment.svc.forge
     notify    → notify (Elixir)       discovered via notify.svc.forge
   NetworkPolicy: order-api↔fulfillment allow; order-api↔notify allow;
                  fulfillment↔notify DENY (proves enforcement)
```

## 4. Manifests (illustrative — `54.02`/`54.03`/`54.05`)

```yaml
kind: Workflow
metadata: { name: order-saga, project: orderpipe }
spec:
  steps:
    - { name: validate, service: order-api,    onError: fail }
    - { name: charge,   service: order-api,    retries: 3, compensate: refund }
    - { name: fulfill,  service: fulfillment,  retries: 2, compensate: cancel-fulfillment }
    - { name: notify,   service: notify,       retries: 5 }
---
kind: NetworkPolicy
metadata: { name: orderpipe-mesh }
spec:
  allow:
    - { from: order-api,   to: fulfillment }
    - { from: order-api,   to: notify }
  default: deny            # fulfillment↔notify therefore blocked
```

Services address peers as `http://fulfillment.svc.forge:8080` (Discovery/DNS) — **never** a
compose service name. `tests/e2e` contract check greps product source for hard-coded peer DNS.

## 5. Data model

```text
orders(id, customer_email, status[placed|validated|charged|fulfilled|notified|failed|refunded],
       total_cents, created_at, updated_at)
order_items(id, order_id → orders.id, sku, qty, unit_cents)
saga_events(id, order_id, step, outcome[ok|retry|compensated], at)     # audit mirror of events
```

## 6. E2E scenario (`tests/e2e/projects/04-orderpipe/spec.ts`)

1. Open `shop.orderpipe.localhost`, add items to cart, **place order**.
2. Watch the order detail page advance through statuses **placed → validated → charged →
   fulfilled → notified** (each driven by a workflow step + event). Assert terminal `notified`.
3. **Failure path:** place a second order flagged to fail the charge step (fixture toggle, e.g.
   a "declined" test card). Watch: status reaches `charged`-attempt, **retries**, then the saga
   **compensates** → order ends `refunded`/`failed` cleanly (no half-fulfilled state).
4. **Network policy proof:** trigger (via a debug endpoint) a call from `fulfillment` → `notify`
   (a **denied** pair) → the call is blocked; assert the block surfaces (error/metric), while
   `order-api`→`fulfillment` (allowed) succeeds.

### Platform assertions (→ findings)
* Discovery resolves each peer to a Ready endpoint; no hard-coded DNS (contract check passes).
* NetworkPolicy actually blocks the denied pair and allows the permitted pairs (deny metric/event
  observed).
* Workflow performs the configured retries and runs compensation on terminal failure; no lost or
  duplicated side effects (idempotent steps).
* Order events are ordered/consistent enough that final DB state matches the saga outcome.

## 7. Likely findings hotspots

Saga retry/compensation semantics and idempotency, event ordering/delivery under failure,
discovery endpoint readiness/staleness, NetworkPolicy enforcement gaps or false-denies,
cross-service trace propagation.

## 8. Acceptance criteria

* `make demo DEMO=54` + `04-orderpipe` E2E pass headed and headless.
* Happy path reaches `notified`; failure path retries then compensates to a clean terminal state.
* Services communicate only via Discovery; denied network pair is blocked, allowed pairs work.
* Zero blocker findings attributed to OrderPipe.

## 9. Steps → see epic

`54.01` scaffold (multi-service) · `54.02` discovery wiring · `54.03` network policy · `54.04`
event choreography · `54.05` workflow saga + retry/compensation · `54.06` E2E browser spec ·
`54.07` demo + gate. Details: [epic 54](../../implementation/epics/54-demo-orderpipe.md).
