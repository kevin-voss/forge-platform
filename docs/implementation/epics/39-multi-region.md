# Epic 39: Multi-region

## Status

Planning

## Milestone

**M3 — Global platform.** Second of the six M3 epics (38–43); 43 is the M3 exit capstone.

## Goal

Let one Forge installation manage workloads spread across multiple regions and multiple providers at once. When this epic is done, a `Region` resource maps a logical region name to one or more `InfrastructureProvider`s, placement policies can require workloads to run in specific regions or a minimum number of regions, service discovery and Gateway routing are region-aware, databases and object storage carry region topology and replication awareness, and a regional health-check failure moves public traffic away from the failed region automatically. Proven by `demos/39-multi-region`.

## Why this epic exists

Every prior epic assumes a single logical region. Real production platforms need geographic spread for latency, redundancy, and data-residency reasons — and, per the platform's core promise, that spread must work across providers (a region can mix Hetzner and AWS nodes) without the product manifest ever naming a provider or region id. This epic introduces `Region` as the resource that makes cross-provider geographic topology declarative rather than an operator's private inventory.

## Relationship to shipped epics

Extends **epic 05 Forge Gateway** with regional gateway instances and traffic steering, **epic 21 Forge Discovery** (M1) with region-aware service-discovery zones, **epic 29 database high availability** with cross-region replica topology awareness, **epic 31 distributed object storage** with region replication, and **epic 23 Forge Infrastructure** (M1) with a new cluster-scoped `Region` kind alongside its existing `NodePool`/`InfrastructureProvider` kinds. Compatibility rule: `Region` is additive; a single-region install — the default for every gate demo through epic 38 — is simply `regions: [default]` with no behavior change to any existing manifest or resource.

## Primary code areas

* `services/forge-discovery/` — region-aware discovery zones (extends its M1 service-discovery baseline)
* `services/forge-gateway/` — regional gateway instances, traffic steering, latency-based routing
* `services/forge-infrastructure/` — `Region` resource, region-to-provider mapping
* `demos/39-multi-region/` — cross-region topology + regional failover acceptance

## Suggested language

Go, matching Discovery, Gateway, and Infrastructure's existing languages.

## Spec references

* `docs/architecture/standalone-cloud.md` § Multi-region
* `specs.md` → Step 05 (Forge Gateway) — the routing baseline gaining region awareness
* `docs/implementation/MASTER_PLAN.md` — epic 21/23/29/31 baselines this epic extends

## Dependencies

* Epic [`05-forge-gateway`](05-forge-gateway.md) — routing layer gaining regional instances and traffic steering
* Epic `21-forge-discovery` (catalogued, not yet materialized) — service discovery gaining region-aware zones
* Epic `23-forge-infrastructure` (catalogued, not yet materialized) — provider/node-pool model gaining the `Region` kind
* Epic `29-database-high-availability` (catalogued, not yet materialized) — database topology awareness across regions
* Epic `31-distributed-object-storage` (catalogued, not yet materialized) — object replication across regions
* Epic `34-dns-and-certificates` (catalogued, not yet materialized) — region-aware and failover DNS records

## Out of scope for this epic

* Cost-optimized region/provider selection (epic 41 — this epic places by policy, not by price)
* GPU-specific multi-region placement (epic 38 remains single-region; this epic does not add GPU topology awareness)
* Plugin-based provider adapters beyond the built-in ones (epic 43)
* A visual topology map (surfaces read-only in epic 40's Console)

## Portability contract

A product manifest declares only logical `regions` and `dataResidency` requirements — never a provider region id, availability zone, or cloud-specific network identifier. A `Region` resource is what maps a logical name to concrete `InfrastructureProvider`s, so the same manifest is provider-portable within a region: `eu-central` might mean AWS `eu-central-1` nodes plus Hetzner Falkenstein nodes behind one regional gateway. Locally, multi-region is simulated with multiple Compose node groups tagged with distinct region labels on one host (the CI default); bare metal, Hetzner, AWS, and Azure use the identical `Region`/`NodePool` resources with real geographic separation.

```yaml
apiVersion: forge.dev/v1
kind: Region
metadata:
  name: eu-central
spec:
  providers: [aws-eu-central-1, hetzner-fsn1]
  gateway: { public: true }
---
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: invoice-api
spec:
  placement:
    regions: [eu-central, eu-west]
    minimumRegions: 2
    dataResidency: { allowedCountries: [DE, IE, NL] }
```

## Success demo

```bash
make demo DEMO=39
```

```text
Topology: eu-central (AWS + Hetzner nodes + regional gateway),
          eu-west (Azure nodes + regional gateway)
Deploy invoice-api with placement.regions: [eu-central, eu-west],
  minimumRegions: 2
→ scheduler places replicas satisfying region + provider spread
Global health check on eu-central's regional gateway starts failing
→ public DNS removes the eu-central endpoint (epic 34 integration)
→ traffic moves to eu-west
→ eu-west's regional autoscaler adds capacity to absorb the shift
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 39.01 | `Region` resource + provider-independent zones | Cluster-scoped kind mapping logical region to providers |
| 39.02 | Region-aware scheduling + placement policy | `regions`/`minimumRegions`/`dataResidency` on `Application` |
| 39.03 | Global service discovery + regional gateways | Cross-region discovery zones; per-region gateway instances |
| 39.04 | Database topology + storage replication awareness | Databases/buckets know their region spread |
| 39.05 | Traffic steering + latency-based routing | Route requests to the nearest healthy region |
| 39.06 | Data-residency policy enforcement | Reject placement violating `allowedCountries` |
| 39.07 | Regional disaster failover | Health-check-driven DNS/traffic failover between regions |
| 39.08 | Demo `39-multi-region` + epic gate | Cross-region topology + failover acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* A single-region install is the implicit default (`regions: [default]`); no existing demo (00–38) needs to change to keep passing once this epic ships.
* Region-to-provider mapping is an operator-owned resource, never something a product manifest author edits directly.
* Latency-based routing uses measured or configured regional latencies, not real-time BGP-style routing — a documented approximation for a self-hosted platform.
* Data-residency enforcement is advisory-then-blocking: epic 33 Policy is the actual enforcement point; this epic defines the fields Policy evaluates.

## Open questions

* How many regions must the local Docker demo simulate to prove the concept without exhausting CI resources? Assumption: two simulated regions (matching the eu-central/eu-west example), each a labeled Compose node group.
* Does a regional gateway have its own public IP/DNS name, or share one global entry point with regional backends? Assumption: each region gets its own regional gateway + DNS name; a global layer (epic 34) does the failover redirection between them.
* Is cross-region replication synchronous or asynchronous for storage/database? Assumption: asynchronous by default (documented RPO), matching epic 29/31's own replication model; synchronous cross-region is out of scope as prohibitively slow for a self-hosted default.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **39.01 — `Region` resource + provider-independent zones** first: every later step (scheduling, discovery, gateway, storage/database awareness) needs the `Region` kind to exist first.
