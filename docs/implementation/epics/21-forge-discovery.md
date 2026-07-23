# Epic 21: Forge Discovery

## Status

In progress

## Milestone

**M1 — Standalone cloud core** (epics 20–25): one manifest runs unchanged on Docker, bare metal, Hetzner, AWS, Azure; nodes and workloads autoscale; workloads survive node loss.

## Goal

Stand up Forge Discovery — a Go service on port `4109` — as the platform's authoritative directory of live service endpoints. When this epic is done, every replica Runtime starts registers a leased `Endpoint` under a `Service` (both new declarative resource kinds, epic 20), leases expire to `Unready` when a replica or its node stops responding, only `Ready` endpoints are ever handed out, callers can resolve `<service>.<environment>.<project>.svc.forge` through an authoritative internal DNS zone or the discovery HTTP/SSE API, and Forge Gateway can source its route table from Discovery behind a reversible flag instead of the epic-05 Control/Runtime sync. Proven by `demos/21-service-discovery`.

## Why this epic exists

Today only Forge Gateway learns where workloads live, and it learns it two ways at once: polling Control's (currently absent) `/v1/endpoints` read model, with Runtime's `/v1/node/state` plus a Control project-tree join as an interim fallback (`services/forge-gateway/internal/sync/endpoints.go`). That coupling is fine for one consumer, but it does not scale to a platform: multi-node placement (epic 08), autoscaling (epic 24), and workload-to-workload calls that never pass through the Gateway edge all need a single source of truth for "which addresses are alive right now," queryable by API, watch, and DNS. Discovery generalizes what Gateway's sync package already does into a first-class platform primitive — a lease-based registry plus an internal DNS zone — so any product or platform service can find a peer without knowing Runtime or Control internals.

## Relationship to shipped epics

* **Epic 04 (Forge Runtime)** — additive call only. Runtime already reports workload state to Control (`04.07`) and probes readiness (`04.04`); this epic adds one new outbound call from Runtime to Discovery after a replica's first successful readiness probe, plus a renew call on the existing probe cadence. No existing Runtime↔Control contract changes.
* **Epic 05 (Forge Gateway)** — facade. The `sync.Source` interface (`services/forge-gateway/internal/sync/endpoints.go`, `sync.go`) gains a third implementation (`DiscoveryEndpointsSource`); `FORGE_ROUTE_SOURCE` gains a third value (`discovery`) alongside `control`/`runtime`. Default stays `control` until parity is proven; flipping is one env var. `routes.Table`, `DeriveRoutes`, `ApplyHostPattern`, and the existing `ControlEndpointsSource`/`RuntimeInterimSource`/`FallbackSource` are never modified, and their existing tests (05.02–05.07) must keep passing unchanged.
* **Epic 07 (Deployment reconciliation)** — observer only. The reconciler's replica create/stop lifecycle is the trigger for Discovery registration/deregistration; this epic does not touch the rolling-update algorithm, rollback, or deployment history.
* **Epic 08 (Multi-node scheduler)** — reused, not reimplemented. Discovery watches the `Node` fleet resource's `Reachable` condition (owned by epic 08) instead of tracking node heartbeats itself; "one primary controller per kind" stays intact.
* **Epic 20 (Declarative resource API)** — new resource kinds. `Service` and `Endpoint` are registered kinds on Control's generic kind registry; Discovery is their primary controller (owns `status`), following the same envelope, `resourceVersion`, and watch conventions every other kind uses. No new API shape is invented.

Compatibility rule: **facade** for Gateway, **additive outbound call** for Runtime, **new resource kind** for the declarative API — never a rewrite of shipped code or a breaking change to a shipped contract.

## Primary code areas

* `services/forge-discovery/` — new Go service: HTTP registration/query API, SSE watch, DNS resolver, Postgres-backed lease store (port `4109`; DNS `5053/udp`)
* `services/forge-gateway/internal/sync/` — additive `DiscoveryEndpointsSource` + `FORGE_ROUTE_SOURCE=discovery` (21.05 only)
* `services/forge-runtime/` — additive endpoint registration/renew/deregister calls (21.02 only)
* `demos/21-service-discovery/` — replica start → resolve → node loss → replacement → resume
* `contracts/openapi/forge-discovery.openapi.yaml` — new contract file

## Suggested language

Go. Discovery needs to run a UDP DNS resolver and a lightweight, low-latency registration/query/watch API; Go keeps it consistent with its primary consumer (Forge Gateway, also Go) and matches `specs.md` §4's preference for Go on infrastructure-facing edge services.

## Spec references

* `docs/architecture/standalone-cloud.md` § Service Discovery & Internal DNS (introduced with the M1 epics; this epic is the first to populate that section)
* `specs.md` → §1 Vision, "service discovery" (line 27)
* `specs.md` → Step 04: Forge Runtime (workload readiness/status this epic reports from)
* `specs.md` → Step 05: Forge Gateway (route sync this epic extends)
* `specs.md` → Step 08: Multi-node scheduler (node fleet/heartbeat this epic reuses)
* `docs/implementation/MASTER_PLAN.md` → Epic 21 catalog + port `4109` / DNS `5053/udp` reservation

## Dependencies

* Epic [`20-declarative-resource-api`](20-declarative-resource-api.md) — resource envelope, kind registration, generic CRUD + watch API that `Service`/`Endpoint` are registered against (**required**)
* Epic [`04-forge-runtime`](04-forge-runtime.md) — replica start/stop and readiness probing (`04.03`–`04.04`) that trigger registration
* Epic [`05-forge-gateway`](05-forge-gateway.md) — the `sync.Source` facade this epic extends
* Epic [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — replica lifecycle that drives register/deregister timing
* Epic [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — `Node` fleet resource and `Reachable` condition this epic watches
* Epic [`22-forge-network`](22-forge-network.md) — **optional**: when its overlay fabric is active, `Endpoint.spec.address` carries the overlay address instead of the plain container IP; Discovery does not require epic 22 to function

## Out of scope for this epic

* Provisioning the overlay/private network fabric itself (epic 22 owns the fabric; this epic only consumes whatever address it is given)
* Node/workload autoscaling decisions (epic 24)
* Weighted, canary, or percentage-based traffic splitting (epic 27 deployment strategies)
* Cross-region discovery federation (epic 39 multi-region)
* Public-facing domains, external DNS delegation, or TLS certificates (epic 34)
* Workload/node mTLS certificate issuance (epic 09 issues identities; this epic only consumes them for endpoint-to-endpoint trust)
* Node capacity accounting or placement strategy (epic 08 scheduler's concern; `Endpoint` carries an address, not resource requirements)

## Portability contract

* A product manifest **must never** contain: internal endpoint IP addresses, `.svc.forge` records, DNS resolver IPs, node identifiers, or overlay network ids. The only discovery-related field a manifest may set is `spec.aliases` on a `Service` (additional names Discovery/Gateway also serve).
* **Local Docker**: `Endpoint.spec.address` is the container's IP on the Compose network; Discovery's resolver listens on `5053/udp` and forwards everything outside `.svc.forge` to Docker's embedded DNS (`127.0.0.11`) so ordinary name resolution is unaffected.
* **Bare metal / Hetzner / AWS / Azure**: identical `Service`/`Endpoint` model; `address` is the node's private or overlay IP (epic 22) reachable over the platform's private network; each node's resolver forwards `.svc.forge` to Discovery over that private network and everything else to the node's normal resolver.
* No provider-managed DNS service (Route 53, Azure DNS, Cloud DNS) is ever required for internal resolution — those appear only as optional adapters under epic 34 for public zones, never for `.svc.forge`.

## Success demo

```bash
make demo DEMO=21
```

```text
demos/21-service-discovery
  Start 2 replicas of demo-echo (project=demo, environment=local)
  → each registers an Endpoint; leases active; Service auto-vivified
  → GET /v1/projects/demo/environments/local/services/demo-echo/endpoints → 2 Ready
  → dig @127.0.0.1 -p 5053 demo-echo.local.demo.svc.forge → 2 A records
  → curl through Gateway (FORGE_ROUTE_SOURCE=discovery) reaches both replicas

Failure flow (the epic gate):
  runtime heartbeat expires → node marked Unreachable (epic 08)
    → Discovery marks every Endpoint on that node Unready, one transaction
    → Gateway (21.05) stops routing to them
    → Scheduler (epic 08) creates replacement replicas
    → Runtime registers new Endpoints with Discovery (21.02)
    → traffic resumes; DNS and Gateway both reflect the replacement within one TTL/sync cycle
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [21.01](../steps/21-forge-discovery/21.01-skeleton-and-registry-model.md) | 140 | Service skeleton + Service/Endpoint resource model | Complete | New Go service, port 4109; kind registration; persistence choice |
| [21.02](../steps/21-forge-discovery/21.02-endpoint-registration-and-leases.md) | 141 | Endpoint registration + TTL leases | Complete | Runtime registers/renews; node-loss transactional unready |
| [21.03](../steps/21-forge-discovery/21.03-readiness-selection-and-watch.md) | 142 | Readiness-aware selection + endpoint watch | Complete | Ready-only reads, SSE watch, client library |
| [21.04](../steps/21-forge-discovery/21.04-internal-dns-zone.md) | 143 | Internal authoritative DNS for `.svc.forge` | Complete | A/AAAA/SRV, TTLs tied to lease state, local resolver wiring |
| [21.05](../steps/21-forge-discovery/21.05-gateway-and-client-integration.md) | 144 | Gateway integration + aliases | Not started | Flagged Gateway source; epic 05 tests unchanged |
| [21.06](../steps/21-forge-discovery/21.06-demo-21-service-discovery.md) | 145 | Demo `21-service-discovery` + epic gate | Not started | Full failure-flow acceptance gate |

## Assumptions

* Discovery keeps its **own** Postgres schema (`discovery`, same shared instance as every other service) as the fast, authoritative-for-serving store; the `Service`/`Endpoint` resources mirrored into Control's generic API (epic 20) are an async, eventually-consistent projection for uniform tooling (`forge get`, CLI, UI), not the hot read path. This avoids write-amplifying every lease renewal into Control's generic `resourceVersion`/status update path, and keeps DNS/selection serving even during a brief Control outage.
* Two independent failure-detection paths converge on `Endpoint.status`: (a) a per-endpoint TTL lease renewed directly by Runtime against Discovery, catching individual replica/process failure; (b) Discovery watching the epic-08 `Node` resource's `Reachable` condition, catching whole-node loss and applying it to every endpoint on that node in one transaction — much faster than waiting out N individual lease expiries.
* `Service` resources are auto-vivified by Discovery the first time an `Endpoint` references an unknown service name in a project/environment (idempotent upsert-if-absent). Operators may also declare a `Service` explicitly via epic 20's generic API (needed only to set `aliases` or explicit ports); no changes to epic 02/07 Control code are required either way.
* Runtime registers/renews/deregisters directly against Discovery's own HTTP API (port `4109`), not through Control's generic CRUD, to keep the hot path low-latency. Discovery, as the primary controller for `Endpoint`, is responsible for mirroring accepted writes into Control.
* The `svc.forge` DNS zone is served by Discovery itself on `5053/udp`. Workload containers reach it via an explicit Compose `dns:` override; Discovery forwards anything outside `.svc.forge` to Docker's embedded resolver (`127.0.0.11`) so normal name resolution keeps working unchanged.
* Gateway keeps its existing poll-based `sync.Syncer` model; `DiscoveryEndpointsSource` implements `Fetch`/`Name` like `RuntimeInterimSource` does today. The SSE watch (21.03) exists for future push-based consumers, but Gateway does not adopt it in this epic — minimizes the Gateway-side diff.
* `Endpoint.spec.address` is address-source-agnostic: Discovery stores whatever Runtime reports (plain container IP locally, overlay address from epic 22 when that fabric is active). Discovery does not decide which address family is used.

## Open questions

* Does epic 34 (`forge-dns`) run a second DNS listener, or reuse Discovery's resolver as its internal backend and only add public-zone delegation? **Assumption:** reuse — flagged for epic 34 planning, not resolved here.
* Is `Service` something operators must declare up front? **Assumption:** no — implicit auto-vivification covers the common case; explicit declaration is opt-in for aliases/ports.
* Should `Endpoint` carry CPU/memory/capacity fields? **Assumption:** no — address, node, and revision label only; capacity accounting stays owned by epic 08's placement model, not duplicated here.
* How does Discovery behave during a Control outage — block writes, or degrade? **Assumption:** keep serving reads/DNS from the local store; mirror writes to Control retry with backoff and catch up when Control returns; the serving path never blocks on Control.
* Is there one Discovery instance or one per node? **Assumption:** one logical service for this epic (replicated later, if at all, under epic 35 control-plane HA); each node's local resolver forwards to it over the private network.

## Next step to implement

**[21.05](../steps/21-forge-discovery/21.05-gateway-and-client-integration.md) — Gateway integration + aliases** (`N = 144`).
