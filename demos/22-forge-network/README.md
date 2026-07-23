# Demo 22: Forge Network (epic gate)

End-to-end acceptance gate for epic 22. Proves Forge-owned overlay networking:
three Runtime nodes join with bootstrap tokens, receive overlay CIDRs, converge
a peer mesh, resolve cross-node services through `.svc.forge` to overlay
addresses, enforce `NetworkPolicy` allow/deny with observable denies, and drop
stale peers/endpoints after node loss.

```text
node-a / node-b / node-c join (bootstrap token → lease → peers)
  → WireGuard peer registry converges (fake WG backend in CI)
  → docker transport a↔b; wireguard a↔c (membership override)
  → deploy frontend@a, api@b, echo@c
  → overlay leases + DNS api.production.demo.svc.forge
  → NetworkPolicy allow frontend→api (deny-all default)
  → remove allow → deny metric / network.policy.denied
  → stop node-c → lease release → peers + DNS cleared
```

Local CI uses `FORGE_NETWORK_WG_BACKEND=fake`,
`FORGE_NETWORK_POLICY_BACKEND=fake`, and
`FORGE_NETWORK_ROUTE_BACKEND=fake` so Docker Desktop / constrained hosts do not
need kernel WireGuard or nftables. Compose builds are sequential
(`COMPOSE_PARALLEL_LIMIT=1`).

## Run

From the repository root:

```bash
make demo DEMO=22
```

Expect a final `demo 22 PASSED` line and exit code `0`.

## What this demo checks

* Bootstrap-token join → overlay CIDR + WireGuard public key on each node
* Peer registry: each node sees the other two
* Per-pair transport (`docker` / `wireguard`) via membership
* Workload overlay leases + Discovery DNS A records in `10.100.0.0/16`
* `NetworkPolicy` allow then deny-all with `forge_network_policy_denied_total`
* Node loss: peer exclusion + endpoint Unready + DNS empty

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API |
| `FORGE_NETWORK_URL_HOST` | `http://127.0.0.1:4110` | forge-network API (host) |
| `FORGE_DISCOVERY_URL_HOST` | `http://127.0.0.1:4109` | Discovery API (host) |
| `FORGE_NETWORK_WG_BACKEND` | `fake` | WireGuard apply backend |
| `FORGE_NETWORK_POLICY_BACKEND` | `fake` | nftables apply backend |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `COMPOSE_PARALLEL_LIMIT` | `1` | Sequential Compose builds |

`docker-compose.yml` in this directory overlays the root `compose.yaml`.

## Fixtures

`fixtures/network-policy.yaml` documents the allow-list policy applied by `run.sh`
against forge-network (`POST .../network-policies`).

## Helpers

`lib/verify.sh` — peer status, transport, DNS overlay lookup, lease checks, and
deny-counter assertions.

## Docs

* Epic: [`docs/implementation/epics/22-forge-network.md`](../../docs/implementation/epics/22-forge-network.md)
* Architecture: [`docs/architecture/networking-and-discovery.md`](../../docs/architecture/networking-and-discovery.md)
* Service: [`services/forge-network/README.md`](../../services/forge-network/README.md)
