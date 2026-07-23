# Epic 23: Forge Infrastructure

## Status

In progress

## Milestone

**M1 — Standalone cloud core** (epics 20–25): one manifest runs unchanged on Docker, bare metal, Hetzner, AWS, Azure; nodes and workloads autoscale; workloads survive node loss.

## Goal

Stand up **Forge Infrastructure** — a Go service on host port `4111` that turns a declared `NodePool` into real machines and back. When this epic is done, an operator (or, later, the Node Autoscaler in epic 24) sets `NodePool.spec.replicas`, and Forge Infrastructure creates or deletes nodes through a pluggable provider adapter (local Docker, generic SSH, static bare metal, Hetzner, AWS, Azure) until the fleet matches — bootstrapping Forge Runtime onto each machine, joining it to the Forge network, and driving it through `Provisioning → Bootstrapping → Joining → Ready → Draining → Deleting`. Every mutating provider call is guarded by a recorded operation id so retries never double-create or double-bill, and a reconciliation pass detects and removes machines Forge created but lost track of. Proven by `demos/23-local-cloud-simulation`: five Docker-provider nodes serve ten replicas, two node containers are killed outright, and the workloads recover on the survivors while Infrastructure replaces the lost nodes.

## Why this epic exists

Epics 04 and 08 gave Forge a Runtime that registers a node and a scheduler that places workloads across whichever nodes already exist — but nothing in the shipped platform *creates* those nodes. Every demo through epic 19 assumes the operator hand-starts `forge-runtime` containers. A standalone cloud platform (`specs.md` §2, the M1 promise) cannot ship that way: it must turn a declared desired node count into real compute on whatever substrate the operator has — a laptop, a rack of bare metal, or a cloud account — without the product manifest ever naming a provider, machine type, region, or credential. This epic is the layer that owns machine lifecycle so that Scheduler, Autoscaler, and Runtime never have to.

## Relationship to shipped epics

Forge Infrastructure is additive on top of two already-complete contracts; it never rewrites them:

* **Epic 04 (`04.02` node identity + registration/heartbeat) — unchanged, wrapped.** A node Infrastructure creates still generates and persists its own id, still calls the same registration/heartbeat path Runtime has had since `04.02`. Infrastructure never assigns or overrides a Runtime node id; it only creates the machine the Runtime process boots on, then waits for that same registration/heartbeat signal to observe `Ready`. The facade is one-directional: Infrastructure's `Node.status.runtimeNodeId` *records* the id Runtime already generated — it never feeds a value back into Runtime's identity logic.
* **Epic 08 (`08.02` node fleet, heartbeat-based liveness) — extended, not replaced.** The scheduler's existing view of "which nodes exist and how much capacity they have" keeps working exactly as it does today — heartbeat timeout still means offline (`08.05` reschedule-on-node-offline is untouched). Infrastructure adds a superset: the formal, cluster-scoped `Node` resource kind (owned by epic 20's declarative resource API) carries provenance (`nodePoolRef`, `providerNodeId`, provisioning `phase`) alongside the same node id the scheduler already tracks. Deleting a `Node` resource drains it through the scheduler's existing rescheduling path before the machine is destroyed — a new caller of `08.05`'s mechanism, not a change to it.
* **Epic 10 (Forge Secrets) — consumed via reference, never duplicated.** `InfrastructureProvider.spec.credentialsSecretRef` points at a Forge Secrets entry; this epic adds no new credential store and no new encryption. The compatibility rule is a **new resource kind** (`InfrastructureProvider`) that is a *pure consumer* of the existing `secret.read` contract from `10.03`/`10.04`.
* **Epic 02 (Forge Control) conventions — followed, not extended.** Forge Infrastructure is a new, independently deployed Go service (like Runtime, Gateway, Build), not a module inside Control; it follows the same health/readiness, structured-log, and OTEL conventions Control established rather than adding endpoints to Control itself.

No shipped API changes signature or response shape as a result of this epic. Everything here is a new service, a new resource kind, or a reference to an existing one.

## Primary code areas

* `services/forge-infrastructure/` — new Go service (provider interface, controllers, operation ledger, HTTP health/debug API), port `4111`
* `demos/23-local-cloud-simulation/` — Docker-provider node-loss-and-recovery acceptance
* `contracts/openapi/forge-infrastructure.openapi.yaml` — provider-facing debug/operations API surface

## Suggested language

Go — matches `forge-gateway` and `forge-build`'s existing Go service layout (`internal/` packages, `forge.local/services/forge-infrastructure` module) and gives every cloud provider's official SDK (Hetzner, AWS, Azure) first-class support.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Infrastructure (provider adapters, NodePool/Node/InfrastructureProvider resource kinds, cost-safety reconciliation) — authored alongside epic 20; `specs.md` itself stops at Step 19 (`00`–`19` are the shipped platform) and does not describe epics 20+.
* `specs.md` → Step 04: Forge Runtime (node identity/registration/heartbeat, `04.02`) — the contract this epic wraps, not replaces.
* `specs.md` → Step 08: Multi-node scheduler (node fleet, heartbeat liveness, `08.02`/`08.05`) — the contract this epic's `Node` kind extends.
* `specs.md` → Step 10: Forge Secrets and configuration (`10.03`/`10.04`) — the credential path `InfrastructureProvider` consumes.

## Dependencies

* Epic [`20-declarative-resource-api`](20-declarative-resource-api.md) — generic resource envelope/registry (`/v1/{kind-plural}`, watch, status subresource, finalizers) that stores `NodePool`, `Node`, and `InfrastructureProvider`; Infrastructure is a controller against that API, not its own resource store
* Epic [`22-forge-network`](22-forge-network.md) — bootstrap/join tokens and mTLS node identity consumed during the `Bootstrapping`/`Joining` phases (`23.03`)
* Epic [`04-forge-runtime`](04-forge-runtime.md) — node identity/registration/heartbeat (`04.02`) that every provisioned node runs unmodified
* Epic [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — node fleet + reschedule-on-offline (`08.02`, `08.05`) that drain/delete and node-loss recovery build on
* Epic [`10-forge-secrets`](10-forge-secrets.md) — encrypted credential storage referenced by `InfrastructureProvider`
* Epic [`02-forge-control`](02-forge-control.md) — service conventions (health, OTEL, structured logs, Postgres) this service follows

## Out of scope for this epic

* Node Autoscaler policy — *deciding* `NodePool.spec.replicas` from demand (epic [`24-forge-autoscaler`](24-forge-autoscaler.md)); this epic only converges actual nodes to whatever `replicas` already says
* Workload Autoscaler / replica-count policy (epic 24) and placement strategy changes (epic [`25-scheduling-enhancements`](25-scheduling-enhancements.md))
* GPU-aware machine selection (epic [`38-ai-infrastructure-scheduling`](38-ai-infrastructure-scheduling.md))
* Multi-region topology and cross-region networking (epic [`39-multi-region`](39-multi-region.md))
* Any managed platform service (managed Kubernetes, managed queue, managed database, managed app hosting) on any provider — primitives only, per the portability contract below
* DNS/TLS provisioning for node public IPs (epic [`34-dns-and-certificates`](34-dns-and-certificates.md))
* Cost accounting/reporting UI (epic [`41-usage-quotas-and-cost`](41-usage-quotas-and-cost.md)) — this epic only guarantees no orphaned billable resource, not a cost dashboard

## Portability contract

A product manifest (`spec:` block of an `Application`/`Service`) must never contain: a provider name, a machine type, a region id, an IP address, a disk type, a network CIDR, or a credential of any kind. Those fields live exclusively on `NodePool` and `InfrastructureProvider` — cluster-scoped resources the platform operator manages, never the application author.

| Capability | Docker (local/CI) | Bare metal | Hetzner | AWS | Azure |
|---|---|---|---|---|---|
| `createNode` / `deleteNode` | Start/stop/remove a container | Adopt/release from a static inventory (no create/destroy) | `POST/DELETE /servers` | `RunInstances`/`TerminateInstances` | ARM VM PUT/delete |
| `createNetwork` / `deleteNetwork` | Docker bridge network (optional) | No-op (`ErrNotSupported`) | Private Network | VPC + subnet | VNet + subnet |
| `attachDisk` / `resizeDisk` | Docker named volume (best-effort; no quota) | No-op (`ErrNotSupported`) | Volume | EBS volume | Managed Disk |
| `createPublicIP` | No-op (returns host bind address) | No-op (`ErrNotSupported`) | Floating IP | Elastic IP | Public IP address |
| `getPricing` | Zero cost | Zero cost | Hetzner server-type prices | AWS Pricing API (cached) | Azure Retail Prices API |
| Capacity ceiling | Config-defined per machine type | Fixed to inventory size (`InventoryExhausted` condition, never fabricated) | Account quota | vCPU service quota | Subscription vCPU quota |
| Orphan reconciliation | Label-based (`forge.managed=true`) | Not applicable (nothing Forge destroys) | Label-based | Tag-based (`ClientToken` + tags) | Tag-based |

Every provider implements the same 16-method adapter interface (`23.01`); a `NodePool` and its workloads behave identically regardless of which row of this table backs it, which is what `demos/23-local-cloud-simulation` (Docker) and the optional cloud-target demos (`23.07`) both exercise against the same acceptance script.

## Success demo

```bash
make demo DEMO=23
```

```text
Create InfrastructureProvider(docker-local) + NodePool(replicas=5, provider=docker-local)
→ 5 runtime-node containers created, bootstrapped, joined; all 5 Nodes reach Ready
Deploy Application(replicas=10)
→ scheduler (epic 08) spreads 10 replicas across the 5 nodes
docker stop  <2 of the 5 node containers>            # simulate hard node loss
→ missed heartbeats mark 2 Nodes offline/Failed (08.05); Infrastructure deletes them
  and creates 2 replacements to hold replicas=5; reconciler reschedules the lost
  replicas onto the 3 survivors, then onto the replacements once Ready
→ 10/10 replicas Ready again; docker ps shows exactly 5 managed node containers
  (no orphans); the operation ledger shows exactly one create_node per node, ever
```

```bash
FORGE_DEMO_TARGET=hetzner make demo DEMO=23   # opt-in only; never part of the default gate
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [23.01](../steps/23-forge-infrastructure/23.01-skeleton-provider-interface-and-nodepools.md) | 153 | Service skeleton, provider interface, NodePool/Node resources | Complete | Provider interface (16 methods); op-id ledger; inert `NodePoolController` |
| [23.02](../steps/23-forge-infrastructure/23.02-docker-provider-local-nodes.md) | 154 | Local Docker provider (local cloud simulation) | Complete | First real adapter; the CI/demo default provider |
| [23.03](../steps/23-forge-infrastructure/23.03-node-bootstrap-and-join.md) | 155 | Node bootstrap, install, join, drain, delete | Complete | Phase state machine; bootstrap token from epic 22; timeout cleanup |
| [23.04](../steps/23-forge-infrastructure/23.04-ssh-and-bare-metal-providers.md) | 156 | Generic SSH provider + static bare-metal inventory | Complete | Adopt/release semantics; finite-capacity conditions |
| [23.05](../steps/23-forge-infrastructure/23.05-hetzner-provider.md) | 157 | Hetzner Cloud provider adapter | Not started | First real cloud provider; rate limits; teardown ordering |
| [23.06](../steps/23-forge-infrastructure/23.06-aws-and-azure-providers.md) | 158 | AWS EC2 + Azure VM provider adapters | Not started | Two adapters; explicit non-use of managed services |
| [23.07](../steps/23-forge-infrastructure/23.07-demo-23-local-cloud-simulation.md) | 159 | Demo `23-local-cloud-simulation` + epic gate | Not started | Node-loss recovery; optional cloud-target demos; epic gate |

## Assumptions

* Epic 20's declarative resource API is available by the time `23.01` starts: it owns storage, optimistic concurrency, watch, and finalizers for `NodePool`/`Node`/`InfrastructureProvider`. Forge Infrastructure is a **controller** against that generic API (Kubernetes-controller shape) plus one private table it alone owns — the `provider_operations` ledger. It does not run its own copy of NodePool/Node/InfrastructureProvider CRUD.
* Epic 22's bootstrap-token issuance is available by `23.03`. Until then (or if sequencing slips), node join falls back to a shared dev token gated behind `FORGE_AUTH_MODE=dev`, exactly as `09.06` deferred Control authorization — swapped for real tokens without changing the join contract.
* `NodePool.spec.replicas` is set by a human operator (`forge apply -f nodepool.yaml`) for the whole of this epic; epic 24 is the first to write `replicas` programmatically. Infrastructure's reconcile loop does not care who set it.
* One `InfrastructureProvider` per NodePool (a pool cannot span providers); an operator wanting mixed capacity runs multiple pools.
* Local Docker is the default provider for every demo and for CI; cloud providers are always opt-in via `FORGE_DEMO_TARGET` and never gate `make demo DEMO=23` or any other epic's demo.
* Operation ids are formatted `op_<ULID>` and are generated and persisted *before* the provider call, matching the `id`-prefixing convention already used for other resource kinds (`app_…`, `dpl_…`).

## Open questions

* **Where do `NodePool`/`Node`/`InfrastructureProvider` really live if epic 20 slips relative to epic 23?** Assumption: Infrastructure's registry client talks to a generic `/v1/{kind-plural}` contract; if epic 20 is not ready, Forge Control temporarily hosts that contract for these three kinds behind the identical HTTP shape, and Infrastructure's controller code does not change when epic 20 takes over — only the base URL does.
* **Is `InfrastructureProvider` itself org-scoped or truly global?** The brief's cluster-scoped kind list places it (like `Node`/`NodePool`) outside the project/environment axis. Assumption: `InfrastructureProvider` is platform-operator-owned and global; multi-tenant provider isolation (one Hetzner account per organization) is modeled by running separate `InfrastructureProvider` resources with distinct `credentialsSecretRef`s, not by scoping the kind itself.
* **How does a project-scoped secret back a cluster-scoped `InfrastructureProvider`?** Assumption: Forge Secrets gains a reserved `platform` project/environment for operator-owned credentials (provider tokens, SSH keys); `credentialsSecretRef` always resolves within that reserved scope, never a tenant project.
* **Does the Docker provider attach node containers to the existing Compose network or a new one?** Assumption: the existing `forge-platform` Compose network, so Control/Gateway reach new node containers by container name exactly as they reach the current single `forge-runtime` container — no new networking primitive needed for the local provider.

## Next step to implement

**[23.04](../steps/23-forge-infrastructure/23.04-ssh-and-bare-metal-providers.md) — Generic SSH provider + static bare-metal inventory**.
