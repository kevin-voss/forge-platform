# Epic 55: Demo 5 — PulseBoard (autoscaling under load + observability)

## Status

In progress

## Goal

A live metrics-dashboard product that proves autoscaling under load and observability surfacing:
HTTP **request-rate autoscaling** grows API replicas, **Infrastructure** adds a Docker node when
the cluster runs out of room, and **Observe** exposes replica count / RPS / p95 that the dashboard
displays — verified by a headed browser E2E where replicas visibly scale up under load (matching
Grafana) and back down when load stops.

## Why this epic exists

TaskFlow proves steady-state serving; PulseBoard proves the platform reacts to load. It ties epics
23/24 (already gated internally by `demos/24-autoscaling`) to a real app and a browser, and makes
autoscaling watchable. Full design:
[`../../demo-projects/projects/05-pulseboard.md`](../../demo-projects/projects/05-pulseboard.md).

## Primary code areas

* `demos/55-pulseboard/` — dashboard SPA + api (Go), NodePool + ScalingPolicy docs, load generator,
  scripts.
* `tests/e2e/projects/05-pulseboard/spec.ts`.

## Suggested language

Go (API) + minimal live dashboard SPA; harness-driven load generator.

## Spec references

* `docs/demo-projects/projects/05-pulseboard.md`
* Epics 24 (autoscaler — httpRequests + node), 23 (infrastructure), 12 (observe), 05 (gateway).

## Dependencies

* Epic **50** (harness) complete.
* Autoscaler, Infrastructure, Observe, Gateway available; Docker NodePool configurable.

## Out of scope for this epic

* Database/persistence (PulseBoard is intentionally stateless).
* Provider node scaling beyond local Docker.

## Success demo

`make demo DEMO=55`: start load → dashboard + Grafana show replicas climbing within bounds; push
past capacity → a Docker node is provisioned and extra replicas run; stop load → replicas and the
added node scale back down.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 55.01 | Product scaffold + baseline deploy | Complete | dashboard SPA + api, routes board./api.pulseboard.localhost, health |
| 55.02 | HTTP request-rate autoscaling + load gen | Complete | `ScalingPolicy httpRequests`; harness load generator; replicas rise/fall within bounds |
| 55.03 | Node autoscaling (Infrastructure) | Complete | exceed capacity → Docker node provisioned → replicas Running; drain after |
| 55.04 | Observe surfacing | Not started | dashboard reads replica count/RPS/p95 from Observe; matches Grafana |
| 55.05 | E2E browser spec | Not started | watch replicas up under load (UI+Grafana), optional node leg, scale down after stop |
| 55.06 | Demo + epic gate | Not started | `demos/55-pulseboard`; `make demo DEMO=55`; wired into test-platform-e2e |

Ordering + `N`: [`../steps/55-demo-pulseboard/README.md`](../steps/55-demo-pulseboard/README.md).

## Open questions

* If epic 25 (scheduling enhancements) ships user-visible behaviour, add a scheduled-scaling panel
  here instead of a sixth demo (see project plan §9).
