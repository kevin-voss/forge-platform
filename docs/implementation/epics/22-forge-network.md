# Epic 22: Forge Network

## Status

Planning

## Milestone

**M1 — Standalone cloud core** (epics 20–25): one manifest runs unchanged on Docker, bare
metal, Hetzner, AWS, and Azure; nodes and workloads autoscale; workloads survive node loss.
Forge Network is the connectivity substrate M1 depends on — without a provider-neutral
private network, "one manifest, any install target" collapses the moment two nodes stop
sharing an L2 segment.

## Goal

When this epic is done, every Runtime node joins a private overlay network as part of its
registration handshake: it authenticates with a single-use bootstrap token issued by
Control, generates its own key pair locally, receives a deterministic address out of a
cluster-wide CIDR plan from the new `forge-network` service (port `4110`), and is handed
the WireGuard peer set it needs to reach every other node — over plain Docker bridging
when nodes share a daemon, over the provider's private network when nodes share one, or
over an authenticated WireGuard mesh when they do not. A `NetworkPolicy` resource lets
operators allow or deny paths between services by name, enforced on each node and
observable as explicit deny events. Proven by `demos/22-forge-network`: workloads on
different simulated nodes reach each other by internal name, an unauthorized path is
denied and logged, and a node that restarts rejoins and reconverges without manual
intervention.

## Why this epic exists

Epics 04–08 shipped multi-node placement on the assumption that every Runtime agent sits
on the same Docker bridge network — true for the local demo, false the moment a node is a
Hetzner VM, an EC2 instance, and a bare-metal box in the same cluster. Forge Network closes
that gap: it gives every node a real address in one flat, cluster-wide space regardless of
where it runs, and it gives the platform the join/authenticate/segment primitives
(bootstrap tokens, peer distribution, `NetworkPolicy`) that every later production concern
— HA control plane (35), DNS/certificates (34), multi-region (39) — assumes already exist.

## Relationship to shipped epics

Forge Network touches three shipped surfaces, all additively; no shipped service is
rewritten and no shipped API loses a field or a required behavior.

| Shipped epic | What it extends | Compatibility rule |
|---|---|---|
| [04-forge-runtime](04-forge-runtime.md) (`04.02` node identity) | Adds a network module to Runtime that generates a WireGuard key pair alongside the existing persisted node id, and configures the node's overlay interface. | Additive module (`network.rs`) beside `node.rs`/`heartbeat.rs`; `GET /v1/node` gains optional `wireguardPublicKey`/`overlayAddress` fields, never removes existing ones. |
| [08-multi-node-scheduler](08-multi-node-scheduler.md) (`08.02` node fleet) | Extends the `nodes` table and registration handshake with a join sequence (bootstrap token → verify → network assign → peer distribute) before a node is marked schedulable. | Additive columns (`network_address`, `wireguard_public_key`, `wireguard_endpoint`, `network_membership`) and additive pre-`online` statuses (`pending-network`, `joining`) on the existing `status` enum; a node that never uses a bootstrap token (`docker` mode, single Compose network) still reaches `online` exactly as `08.02` describes. |
| [05-forge-gateway](05-forge-gateway.md) | Upstream resolution gains an overlay-address strategy (resolve to a workload's overlay IP instead of a host-published port), used only in `provider-private`/`wireguard` mode. | New resolver strategy selected by `FORGE_GATEWAY_UPSTREAM_RESOLUTION`; the existing host-port resolution stays the default and only path in `docker` mode. |

## Primary code areas

* `services/forge-network/` — new Go service: address allocation, WireGuard peer registry
  + distribution, `NetworkPolicy` evaluation, port `4110`
* `services/forge-runtime/src/network/` — new Rust module: node-side WireGuard interface +
  peer application, `NetworkPolicy` enforcement (nftables); extends epic 04
* `services/forge-control/` — additive `nodes` columns + join-sequence orchestration
  extending `08.02`; bootstrap token issuance/verification
* `services/forge-gateway/internal/` — optional overlay-address upstream resolver
  extending epic 05
* `demos/22-forge-network/` — multi-node private network + policy-deny + rejoin acceptance
* `contracts/openapi/` — new `forge-network.openapi.yaml`

## Suggested language

Go for `services/forge-network`. Justification: (1) precedent — `services/forge-gateway`
is already Go for "networking, reverse proxying, routing" (`specs.md` §4 language matrix);
(2) the WireGuard control-plane ecosystem is Go-first and mature
(`golang.zx2c4.com/wireguard/wgctrl` for kernel peer configuration,
`golang.zx2c4.com/wireguard/device`/`wireguard-go` for the userspace fallback), so one
language drives both paths without a second FFI layer; (3) it keeps Rust's footprint
limited to Runtime's existing host-privileged surface (Docker socket, now also
`wg`/nftables) instead of introducing a second network stack in a second language for the
same concern. Runtime's node-side enforcement extends the existing Rust codebase — no new
language there.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Network (address plan, WireGuard,
  `NetworkPolicy`, provider network modes)
* `specs.md` → Step 04 (Runtime node identity `04.02`, extended here)
* `specs.md` → Step 08 (node fleet + registration `08.02`, extended here)
* `docs/implementation/MASTER_PLAN.md` → Epic 22 catalog + port `4110` reservation

## Dependencies

* Epic [20-declarative-resource-api](20-declarative-resource-api.md) — generic resource
  envelope/API shape that `Network` and `NetworkPolicy` use as kinds
* Epic [21-forge-discovery](21-forge-discovery.md) — publishes the internal DNS names that
  resolve to the overlay addresses this epic assigns (consumed in `22.06`)
* Epic [04-forge-runtime](04-forge-runtime.md) — node identity/registration to extend
  (`04.02`)
* Epic [08-multi-node-scheduler](08-multi-node-scheduler.md) — node fleet + registration
  handshake to extend (`08.02`)
* Epic [09-forge-identity](09-forge-identity.md) — platform auth that gates bootstrap-token
  issuance and every internal `forge-network` API call
* Epic [02-forge-control](02-forge-control.md) — shared Postgres + resource-envelope
  hosting

## Out of scope for this epic

* Internal CA / certificate issuance itself (epic 34-dns-and-certificates) — this epic only
  defines the seam
* DNS delegation / public TLS (epic 34)
* Multi-region routing / latency-aware topology (epic 39-multi-region)
* Autoscaling nodes in or out (epic 24-forge-autoscaler) — this epic only makes a
  newly-joined node reachable
* Bin-packing/placement changes (epic 08 scheduling logic is unchanged)
* A full policy engine beyond L3/L4 allow/deny by service and port (broader admission
  policy is epic 33-forge-policy)

## Portability contract

The product manifest (`Application`, `Service`, dependency blocks) must never contain: IP
addresses or CIDR ranges, WireGuard keys or endpoints, a network transport mode
(`docker`/`provider-private`/`wireguard`), NAT/firewall rules, or a provider VPC/subnet id.
All of that lives in the cluster-scoped `Network` kind and in `NodePool`/
`InfrastructureProvider` resources owned by the platform operator. `NetworkPolicy` names
other resources by application/service/database/queue, never by address.

Behavior per install target (identical service reachability in all five):

| Target | Default transport | Notes |
|---|---|---|
| Local Docker | `docker` | Nodes are containers on one Compose network; overlay addresses/DNS names are still assigned for naming consistency, WireGuard inactive |
| Bare metal | `wireguard` | No shared private network assumed between racks/sites unless the operator declares one |
| Hetzner | `provider-private` | Uses a Hetzner Cloud private network when all nodes are in it; `wireguard` for any node outside it |
| AWS EC2 | `provider-private` | Uses a shared VPC/subnet (a primitive, not a managed network service) when co-located; `wireguard` across VPCs/regions/accounts |
| Azure VM | `provider-private` | Uses a shared VNet when co-located; `wireguard` otherwise |

Mixed clusters pick transport **per node pair** (`22.04`), never per cluster — a
Hetzner+bare-metal cluster uses `provider-private` between Hetzner nodes and `wireguard`
for every pair touching the bare-metal box, transparently to workloads.

## Success demo

```bash
make demo DEMO=22
```

```text
Start 3 simulated Runtime nodes (node-a, node-b, node-c) with distinct bootstrap tokens
→ each generates a key pair, joins via Forge Network, gets an overlay address, receives peers
Deploy service "echo" on node-b
→ a workload on node-a reaches echo.production.demo.svc.forge by internal name (through the overlay)
Apply a NetworkPolicy denying node-a's workload → a "restricted" service on node-c
→ the call is refused and a deny event/metric is observable
Restart node-b
→ node-b rejoins with its persisted identity, peers reconverge, traffic to echo resumes
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [22.01](../steps/22-forge-network/22.01-skeleton-and-address-allocation.md) | 146 | Service skeleton + provider-independent address plan | Not started | `forge-network` on `4110`; CIDR plan; leases + reclamation |
| [22.02](../steps/22-forge-network/22.02-node-identity-and-bootstrap-tokens.md) | 147 | Node identity, bootstrap tokens, join handshake | Not started | Extends `04.02`/`08.02` registration |
| [22.03](../steps/22-forge-network/22.03-wireguard-peer-management.md) | 148 | WireGuard peer management + route distribution | Not started | Key registry, peer computation, rotation |
| [22.04](../steps/22-forge-network/22.04-local-and-provider-network-modes.md) | 149 | Local Docker mode + provider private networks | Not started | Per-pair transport selection |
| [22.05](../steps/22-forge-network/22.05-network-policy-resource-and-enforcement.md) | 150 | `NetworkPolicy` resource + enforcement | Not started | Node-level enforcement + observability |
| [22.06](../steps/22-forge-network/22.06-overlay-dns-and-cross-node-services.md) | 151 | Overlay + Discovery/DNS integration | Not started | Cross-node calls by internal name |
| [22.07](../steps/22-forge-network/22.07-demo-22-forge-network.md) | 152 | Demo `22-forge-network` + epic gate | Not started | Join, reach, deny, rejoin |

## Assumptions

* `services/forge-network` is a new Go service on host port `4110`; it owns its own
  Postgres schema (`network`) in the shared instance, following the per-service-schema
  pattern established by `09-forge-identity`.
* The overlay is a single flat cluster-wide address space (default `10.100.0.0/16`,
  `FORGE_NETWORK_CLUSTER_CIDR`), carved into one `/24` per node
  (`FORGE_NETWORK_NODE_PREFIX_LEN=24`); real container IPs come from that per-node `/24`
  via a per-node Docker network, so no port-translation hop is needed once a peer route
  exists.
* IPv4 only in this epic; the `Network` resource reserves an `ipv6Cidr` field (null by
  default) for a later epic to fill in — no IPv6 data-plane work happens here.
* Bootstrap tokens are single-use, scoped (to an organization/`NodePool`), and expire
  (default 15 minutes); they are issued by Control (extending `09-forge-identity`'s token
  machinery) and verified by Control as part of the join handshake, not by `forge-network`
  directly.
* Node private keys are generated on the node (Runtime) and never transmitted anywhere,
  including to Forge Network; only public keys and endpoints cross the wire.
* WireGuard uses the kernel module when present (Linux hosts, most cloud VMs); any host
  without it (Docker Desktop on macOS, restricted CI runners) falls back to a userspace
  implementation (`wireguard-go`/`boringtun`) behind the same peer-management API —
  `FORGE_NETWORK_WG_BACKEND=kernel|userspace|auto` (default `auto`).
* `NetworkPolicy` enforcement happens on the node (Runtime's new network module writing
  nftables rules), not centrally — `forge-network` computes and distributes policy, it does
  not sit in the data path.
* Default policy is `allow-within-environment` / `deny-across-environment`; an explicit
  `NetworkPolicy` narrows traffic within an environment, it does not need to exist for
  same-environment traffic to work at all.

## Open questions

* **Per-workload address stability across reschedule** — does a rescheduled replica keep
  its overlay IP or get a new one from the new node's `/24`? Assumption: the address is
  bound to the node's block, so a reschedule to a different node changes the IP; Discovery
  (`21`) re-publishes it and callers resolve by name, never by address.
* **Bootstrap token issuance surface** — a CLI command, a Control API, or both?
  Assumption: a Control API (`POST /v1/nodes/bootstrap-tokens`) that both the CLI and
  `NodePool` automation call; no separate service.
* **Mesh vs hub WireGuard topology default** — full mesh is simplest to reason about but is
  O(n²) tunnels. Assumption: full mesh by default (fits the single-developer-machine and
  small-cluster scale this platform targets through M1); `22.03` documents a hub-topology
  option for later scale-out but does not implement it.
* **What enforces `NetworkPolicy` in `docker` mode**, where there is no WireGuard hop to
  hang rules on? Assumption: the same nftables enforcement point in Runtime applies
  regardless of transport, matched on the workload's Docker-assigned overlay IP, not on a
  WireGuard interface.

## Next step to implement

**[22.01](../steps/22-forge-network/22.01-skeleton-and-address-allocation.md) — Service
skeleton + provider-independent address plan** (stands up `forge-network` on `4110` with
the CIDR plan and lease model that everything else in the epic assigns nodes and workloads
from).
