# Epic 05: Forge Gateway

## Status

In progress

## Goal

Deliver a Go reverse proxy that exposes Runtime-published workloads through stable host/path HTTP routes so clients never need to know the random runtime ports. When this epic is done, deployed services are reachable via names like `go.demo.localhost` through the gateway on port `4000`; routes are synced from Control/Runtime state, unhealthy upstreams receive no traffic, request IDs and forwarded headers propagate, timeouts are enforced, and WebSocket/SSE connections proxy correctly — all with dynamic route updates that require no restart.

## Why this epic exists

Runtime assigns ephemeral host ports; nothing stable points at a workload. The gateway provides the durable front door the developer experience depends on (`*.demo.localhost`), routes to healthy upstreams only, and is the ingress that later demos (source-to-deploy, full platform) route product traffic through.

## Primary code areas

* `services/forge-gateway/` — Go reverse proxy, route table, sync loop, health-aware balancer (port `4000`)
* `demos/05-routed-service/` — three language demos behind hostnames

## Suggested language

Go (per `specs.md` §4). Standard library `net/http` + `httputil.ReverseProxy` is sufficient; implementers may add a router (chi/gorilla) for host/path matching.

## Spec references

* `specs.md` → Step 05: Forge Gateway
* `specs.md` → §4 Language matrix (Go for Gateway)
* Epic [`04-forge-runtime`](04-forge-runtime.md) → workload published ports + status model
* Epic [`02-forge-control`](02-forge-control.md) → active service endpoints / read models

## Dependencies

* Epic [`04-forge-runtime`](04-forge-runtime.md) — workload published host ports (`04.03`) and readiness/status (`04.04`); actual-state/endpoint data surfaced via Control integration (`04.07`)
* Epic [`02-forge-control`](02-forge-control.md) — service/deployment endpoint data to build routes from
* Epic `00` — Compose, ports

## Out of scope for this epic

* TLS termination / certificates (local `.localhost` over HTTP is enough)
* Authentication at the edge (epic 09 optional gateway auth)
* Rate limiting / WAF
* Rolling traffic shift semantics owned by the reconciler (epic 07 consumes gateway health-awareness)
* Building/deploying the demo apps (epics 01/04/06)

## Success demo

```bash
make demo DEMO=05
```

`demos/05-routed-service` deploys Go, Rust, and Python demo workloads and exposes them through `go.demo.localhost`, `rust.demo.localhost`, and `python.demo.localhost` on the gateway (`4000`). Requests reach the right upstream without the caller knowing runtime ports; stopping a workload removes it from rotation; a route change takes effect without restarting the gateway.

```text
go.demo.localhost      ┐
rust.demo.localhost    ├─→ Forge Gateway :4000 ─→ 127.0.0.1:<ephemeral runtime port>
python.demo.localhost  ┘
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [05.01](../steps/05-forge-gateway/05.01-skeleton-and-health.md) | Skeleton + health | Complete | Go service, port 4000 |
| [05.02](../steps/05-forge-gateway/05.02-route-table-and-proxy-core.md) | Route table + reverse proxy core | Complete | In-memory table, RR proxy, admin API |
| [05.03](../steps/05-forge-gateway/05.03-sync-routes-from-control.md) | Sync routes from Control | Complete | Control endpoints + Runtime interim sync, refresh API |
| [05.04](../steps/05-forge-gateway/05.04-health-aware-upstreams.md) | Health-aware upstreams | Complete | Depends on 05.02/05.03 + Runtime status |
| [05.05](../steps/05-forge-gateway/05.05-request-ids-headers-timeouts.md) | Request IDs, forwarded headers, timeouts | Not started | Depends on 05.02 |
| [05.06](../steps/05-forge-gateway/05.06-websocket-and-sse-proxy.md) | WebSocket + SSE proxy | Not started | Depends on 05.02/05.05 |
| [05.07](../steps/05-forge-gateway/05.07-demo-routed-service-and-gate.md) | Demo `05-routed-service` + gate | Not started | Depends on all prior |

## Assumptions

* Gateway source lives under `services/forge-gateway/`; demo under `demos/05-routed-service/`.
* Gateway listens on host port `4000` (public range); in-container `PORT` default `8080`.
* Host-based routing uses `*.demo.localhost` (resolves to loopback on most systems; documented fallback: `Host` header override or `/etc/hosts`).
* Route source of truth is Control's active endpoints (populated by Runtime `04.07`); the gateway may also read Runtime `/v1/node/state` directly as a documented interim.
* Round-robin balancing across ready replicas; a single replica is the common case in this epic.
* Until Identity `09.06`, no edge auth; `FORGE_AUTH_MODE=dev`.

## Open questions

* **Route source:** read from Control endpoints, Runtime `/v1/node/state`, or both? (Assumption: Control is primary; Runtime state as interim/fallback until Control exposes a stable endpoint read model.)
* **Hostname resolution:** rely on `*.localhost` → loopback, or require `/etc/hosts` entries in the demo? (Assumption: `Host` header in curl for CI; document `/etc/hosts` for humans.)
* **Sync mechanism:** poll vs push/subscribe? (Assumption: poll on an interval + a manual refresh endpoint; event-driven sync deferred to epic 11.)
* **WebSocket/SSE scope:** full duplex WS + SSE, or SSE + contract-tested WS echo if timeboxed? (Assumption: implement both; minimum bar is a WS echo + one SSE stream per `MASTER_PLAN` open question 6.)

## Next step to implement

**[05.05](../steps/05-forge-gateway/05.05-request-ids-headers-timeouts.md) — Request IDs, forwarded headers, timeouts**.
