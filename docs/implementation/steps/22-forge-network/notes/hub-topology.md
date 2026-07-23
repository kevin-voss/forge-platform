# Design note: WireGuard hub topology (deferred)

Step `22.03` implements **full mesh** peer sets: every online node peers with every
other online node, `allowed_ips` = peer's node `/24`. That is O(n²) tunnels.

## Hub option (not implemented)

`FORGE_NETWORK_WG_TOPOLOGY=hub` is accepted as configuration and documented here, but
forge-network falls back to mesh and logs a warning. A future change would:

1. Designate one or more **relay** nodes (operator-selected or Control-elected).
2. Compute peer sets so non-relay nodes peer **only** with relays; relays peer with
   all nodes (and optionally each other).
3. Keep the same distribution API (`GET .../peers`) and `peer_version` bump rules —
   only the `PeerSetComputer` strategy changes.

## When to switch

Defer hub topology until node counts make O(n²) tunnels impractical — roughly
**>50 online nodes** in one Network, or when measured handshake/CPU cost on small
nodes becomes the bottleneck. Until then, mesh keeps routing and failure modes
easier to reason about for the M1 standalone-cloud scale.
