# 0005. Forge owns node addressing and the overlay network

## Status

Accepted (target architecture; introduced by epic 22)

## Context

Nodes may live on one machine as containers, in one provider's private network, or across
several providers and regions. Relying on provider networking would make connectivity,
addressing, and policy behave differently per target — exactly what the platform promise
forbids. Relying only on an overlay would waste the private networking that a single-cloud
installation already has.

## Decision

Forge allocates provider-independent addresses itself and selects a transport **per node
pair**:

* `docker` — all nodes on one machine
* `provider-private` — nodes sharing one provider private network
* `wireguard` — cross-node, cross-provider, bare metal, or untrusted paths

Forge owns WireGuard peer configuration: key registry, peer set computation, incremental
distribution, keepalives, MTU, and rotation. Node private keys are generated on the node
and never transmitted. Bootstrap tokens are single-use, scoped, and expiring.

## Consequences

* Service names resolve identically in every topology, including mixed-provider clusters
* Network policy can reference logical resources (application, database, queue) instead of
  addresses
* Forge takes on responsibility for NAT traversal, MTU, and key rotation correctness
* Local demos must exercise the real path; where the WireGuard kernel module is absent, a
  userspace implementation is used so behaviour stays observable on any laptop
