# Demo 21: Service discovery (epic gate)

End-to-end acceptance gate for epic 21. Proves Forge Discovery is the
authoritative, health-aware directory of live endpoints: Runtime agents register
leases, Ready-only selection and SSE watch work, `.svc.forge` DNS answers from
Ready addresses, and Gateway can source routes from Discovery behind
`FORGE_ROUTE_SOURCE=discovery` (and flip back to `control` without Discovery data
loss).

```text
Runtime A/B start replicas
  → leases registered (Runtime auto + canonical fixture names)
  → GET .../endpoints → Ready only
  → dig @127.0.0.1 -p 5053 demo-echo.local.demo.svc.forge → A records
  → Gateway (FORGE_ROUTE_SOURCE=discovery) routes by Host

Failure:
  Runtime B stopped → lease/node loss → endpoint Unready
  → Ready list / DNS / Gateway exclude it
  → replacement endpoint registers → traffic resumes

Flip:
  FORGE_ROUTE_SOURCE=control → discovery (Discovery store unchanged)
```

This demo sets `FORGE_AUTH_MODE=dev`. Runtimes mount the host Docker socket —
local-dev only. Builds are sequential (`COMPOSE_PARALLEL_LIMIT=1`) for memory-constrained hosts.

## Run

From the repository root:

```bash
make demo DEMO=21
```

Expect a final `demo 21 PASSED` line and exit code `0`.

## What this demo checks

* Three services (`demo-echo`, `users-api`, `orders-api`) with multiple endpoints
* Dual Runtime agents (`node-a`, `node-b`)
* Ready-only list; Unready/expired endpoints excluded
* SSE watch on `demo-echo` endpoints
* Internal DNS: `*.local.demo.svc.forge` (+ `echo` alias)
* Gateway sync from Discovery + alias hostname
* Lease/node-loss failure path after stopping Runtime B
* Gateway `FORGE_ROUTE_SOURCE` flip discovery ↔ control without Discovery data loss

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API |
| `FORGE_DISCOVERY_URL_HOST` | `http://127.0.0.1:4109` | Discovery API (host) |
| `FORGE_GATEWAY_URL` | `http://127.0.0.1:4000` | Gateway edge |
| `FORGE_ROUTE_SOURCE` | `discovery` | Initial Gateway sync source |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `COMPOSE_PARALLEL_LIMIT` | `1` | Sequential Compose builds |

`docker-compose.yml` in this directory overlays the root `compose.yaml`.

## Fixtures

`fixtures/services.yaml` documents the three `forge.dev/v1` Service resources
(including the `echo` alias on `demo-echo`). The gate script registers matching
endpoints against Discovery under `project=demo` / `environment=local`.

## Docs

* Epic: [`docs/implementation/epics/21-forge-discovery.md`](../../docs/implementation/epics/21-forge-discovery.md)
* Service: [`services/forge-discovery/README.md`](../../services/forge-discovery/README.md)
