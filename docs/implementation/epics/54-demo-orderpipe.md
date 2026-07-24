# Epic 54: Demo 4 — OrderPipe (workflows + events + discovery + network)

## Status

In progress

## Goal

A multi-service order-processing product that proves the platform's coordination layer:
a **Workflow** saga drives `validate → charge → fulfill → notify` with retry + compensation,
services communicate by **events** and find each other via **Discovery** (`.svc.forge`), and
**NetworkPolicy** enforces who may talk to whom — verified by a headed browser E2E of a happy-path
order and a failing order that cleanly compensates.

## Why this epic exists

Real systems are many services with a business process spanning them. OrderPipe is the multi-service
counterpart to TaskFlow, exercising workflows, event choreography, service discovery, and network
policy together. Full design:
[`../../demo-projects/projects/04-orderpipe.md`](../../demo-projects/projects/04-orderpipe.md).

## Primary code areas

* `demos/54-orderpipe/` — storefront SPA + order-api (Go) + fulfillment (Python) + notify (Elixir),
  workflow + NetworkPolicy resource docs, scripts.
* `tests/e2e/projects/04-orderpipe/spec.ts`.

## Suggested language

Go (order-api) + Python (fulfillment) + Elixir (notify) + minimal storefront SPA (multi-language on
purpose, mirroring the capstone).

## Spec references

* `docs/demo-projects/projects/04-orderpipe.md`
* Epics 16 (workflows), 11 (events), 21 (discovery), 22 (network), 10 (secrets).

## Dependencies

* Epic **50** (harness) complete.
* Workflows, Events, Discovery, Network, Secrets, managed Postgres available.

## Out of scope for this epic

* Real payment integration (charge is a mock PSP with an injectable failure).
* AI, storage, autoscaling (covered elsewhere).

## Success demo

`make demo DEMO=54`: place an order → status advances to `notified`; place a failing order → saga
retries the charge then compensates to a clean `refunded/failed` terminal state; a denied
service-to-service pair is blocked while allowed pairs succeed; no hard-coded peer DNS.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 54.01 | Multi-service scaffold + Postgres | Complete | order-api + fulfillment + notify + storefront; orders/items schema; baseline deploy |
| 54.02 | Service discovery wiring | Complete | peers resolved via `*.svc.forge`; contract check: no hard-coded DNS |
| 54.03 | Network policy | Not started | overlay + NetworkPolicy allow/deny; denied pair blocked, allowed pairs work |
| 54.04 | Event choreography | Not started | `order.*` events emitted/consumed; status advances via events |
| 54.05 | Workflow saga + retry/compensation | Not started | validate→charge→fulfill→notify; injected charge failure → retry → compensate |
| 54.06 | E2E browser spec | Not started | happy path to `notified`; failure path compensates; network-policy proof |
| 54.07 | Demo + epic gate | Not started | `demos/54-orderpipe`; `make demo DEMO=54`; wired into test-platform-e2e |

Ordering + `N`: [`../steps/54-demo-orderpipe/README.md`](../steps/54-demo-orderpipe/README.md).

## Open questions

* Does forge-workflows own step orchestration + compensation directly, or coordinate via events
  only? Confirm in `54.05`; model the saga to the actual contract and record gaps as findings.
