# Networking, discovery, and identity of nodes

**Status:** Target model, introduced by epics `21` (Forge Discovery) and `22` (Forge
Network), completed by epic `34` (DNS and certificates).

Forge must connect nodes and workloads **the same way** on a laptop, a rack, one cloud, or
several clouds at once. Provider networking is used when it helps and is never required.

---

## 1. Layers

```text
Workload      talks to  users.production.shop.svc.forge
   ↓
Discovery     resolves the name to Ready endpoints
   ↓
Forge Network gives every node and workload a provider-independent address
   ↓
Transport     Docker bridge · provider private network · WireGuard
```

A workload only ever knows a name. Which of the three transports carries the packet is a
platform concern.

---

## 2. Naming

```text
<service>.<environment>.<project>.svc.forge
```

```text
users.production.shop.svc.forge
postgres.production.shop.svc.forge
events.production.shop.svc.forge
```

Names are stable across replicas, nodes, providers, and regions. Nothing in a product
manifest ever contains an IP address or a port of another service.

---

## 3. Discovery

Discovery keeps a registry of `Service` and `Endpoint` resources.

```text
Runtime starts a users-api replica
→ Runtime allocates an overlay address from Forge Network (workload lease)
→ Runtime reports that overlay endpoint to Discovery
→ Discovery registers the endpoint (lease with TTL)
→ health probe succeeds
→ endpoint becomes Ready
→ Gateway and internal DNS include it (Ready + current overlay lease only)
```

Internal `.svc.forge` answers never include provider public IPs. Split-horizon keeps
`.svc.forge` on Discovery; public customer domains remain epic `34`.

Failure is the interesting half:

```text
Runtime heartbeat expires
→ node becomes Unreachable
→ all endpoints on that node become Unready in one transaction
→ Gateway stops routing to them
→ Scheduler creates replacement workloads
→ Discovery registers the replacement endpoints
→ traffic resumes
```

Endpoint state is lease-based, so a partition degrades to "no traffic to unreachable
replicas" rather than "traffic into a black hole". Consumers can watch endpoints instead
of polling, using the same watch semantics as every other resource.

---

## 4. Addressing

Forge allocates addresses itself so they survive provider differences:

```text
installation range   →  per-node range  →  per-workload address
```

Allocations are resources with leases: a deleted node returns its range, and a
re-registered node gets its range back if it is still leased. Ranges are chosen to avoid
collisions with common provider subnets and Docker's default bridge networks; the plan is
IPv4-first with room for dual-stack.

---

## 5. Transports

| Mode | When | Notes |
|---|---|---|
| `docker` | all nodes are containers on one machine | Docker networks; no encryption layer needed locally |
| `provider-private` | nodes share one provider private network | lowest overhead in a single-cloud installation |
| `wireguard` | cross-node, cross-provider, bare metal, or anything untrusted | Forge owns peer configuration |

A mixed fleet picks the transport **per node pair**: two AWS nodes in one VPC can talk
directly while the Hetzner node reaches both over WireGuard.

Forge — not the operator — owns WireGuard peer configuration: key registry, peer set
computation, incremental distribution, keepalives, MTU, and key rotation without dropping
established traffic.

---

## 6. Node identity and join

```text
New machine starts
→ Forge Runtime receives a single-use, scoped, expiring bootstrap token
→ Runtime generates its own key pair (the private key never leaves the node)
→ Control verifies the bootstrap token
→ Forge Network assigns the node address and range
→ WireGuard peer configuration is distributed
→ node joins the private Forge network
→ node registers and becomes schedulable
```

The node key pair is the root of node identity: it backs the node certificate (epic `34`),
mutual TLS between platform services, and the workload-identity check that Forge Secrets
performs before delivering a secret.

---

## 7. Network policy

```yaml
apiVersion: forge.dev/v1
kind: NetworkPolicy

metadata:
  name: invoice-api-policy

spec:
  target:
    application: invoice-api

  ingress:
    - from: { service: forge-gateway }
      ports: [8080]

  egress:
    - to: { database: invoice-db }
      ports: [5432]
    - to: { queue: invoice-jobs }
      ports: [4222]
```

Policies reference **logical resources**, not addresses. Segmentation follows the tenancy
tree — organization, project, environment — and denies are observable: a blocked call
produces a counted, labelled event, because a silent drop is an unfixable bug.

---

## 8. Public entry

```text
User attaches api.example.com
→ Forge verifies domain ownership
→ DNS adapter creates the validation record
→ Certificate service requests an ACME certificate
→ certificate is issued and stored as a secret
→ Gateway loads the certificate
→ public route becomes Ready
```

Domain *registration* stays external. After delegation, Forge owns the zone: service
discovery zones, public mappings, weighted records, failover records, and region-aware
records all come from the same DNS controller.

---

## 9. Overlay DNS contract (22.06)

```text
nameserver <forge-dns-overlay-ip>
search production.shop.svc.forge
```

```text
users.production.shop.svc.forge -> A/AAAA for Ready overlay endpoints only
```

Runtime bootstraps this resolver config on the node. If DNS apply fails, the previous
config is kept and the node is marked `Degraded`. Network and Discovery reconcile
endpoint addresses against active workload leases and observed routes; drift increments
`forge_network_route_drift_total` and is logged with
`{endpoint_id, expected_overlay_ip, observed_route}`. DNS resolution is observed as
`forge_network_dns_resolution_total{result}` and span `network.dns.resolve`.
NetworkPolicy denies from step `22.05` still apply after a name resolves.

## 10. Local fidelity (proven)

Proven by `make demo DEMO=22` (`demos/22-forge-network`) on one developer machine
with Docker. Fake WireGuard / nftables / route backends stand in for kernel
features on Docker Desktop and CI; the control plane and DNS contracts are the same.

Local path exercised by the gate:

```text
bootstrap tokens → node leases (10.100.x.0/24) → peer registry
→ docker transport (colocated) / wireguard (membership override, fake WG)
→ workload overlay leases → Discovery DNS A (overlay only)
→ NetworkPolicy allow then deny-all + forge_network_policy_denied_total
→ node leave releases lease → peers and DNS answers drop
```

* three runtime-node containers join a Forge network with overlay addresses
* a workload on node A resolves a service on node B by `.svc.forge` to an overlay IP
* a `NetworkPolicy` deny path is observable (metric + `network.policy.denied`)
* a stopped node is removed from the peer set and from Ready DNS answers

If a networking feature cannot be shown locally, it cannot be trusted in production —
and it will not pass its epic gate.
