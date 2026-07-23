# forge-network

Provider-independent cluster overlay address plan (epic 22). Host port `4110` /
container `8080`.

Step `22.01` stands up the service skeleton, Postgres-backed `Network` resources,
and node/workload address leases carved from a cluster CIDR (default
`10.100.0.0/16` → one `/24` per node → one address per workload).

Step `22.03` adds WireGuard peer registry, full-mesh peer-set computation,
incremental `peer_version` bumps, dual-key rotation, and Runtime distribution
(`GET .../peers`, `rotate-key`, `applied-version`).

## Quick start

```bash
# From repo root
make -C services/forge-network run

curl -sf localhost:4110/health/live | jq
curl -sf localhost:4110/health/ready | jq

curl -s -X POST localhost:4110/v1/networks \
  -H 'content-type: application/json' \
  -d '{"name":"cluster-overlay","spec":{"clusterCidr":"10.100.0.0/16","nodePrefixLength":24}}' | jq

curl -s -X POST localhost:4110/v1/networks/cluster-overlay/node-leases \
  -H 'content-type: application/json' -d '{"node_id":"node-a"}' | jq
# → 10.100.1.0/24 (index 0 reserved)

curl -s -X POST localhost:4110/v1/networks/cluster-overlay/workload-leases \
  -H 'content-type: application/json' \
  -d '{"node_id":"node-a","workload_id":"wl_1"}' | jq
```

## Address plan

| Layer | Default | Notes |
|---|---|---|
| Cluster CIDR | `10.100.0.0/16` | `FORGE_NETWORK_CLUSTER_CIDR` |
| Node block | `/24` | `FORGE_NETWORK_NODE_PREFIX_LEN`; first block (`.0.0/24`) reserved |
| Gateway | `.1` | Per node block |
| Workload | `.2`–`.254` | Sequential; idempotent per `workload_id` |

Overlaps with Docker bridge/IPAM subnets (via Docker Engine API) or
`FORGE_NETWORK_PROVIDER_CIDRS` are refused (`CidrCollision`, Network `Failed`).

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | Listen port (host-mapped `4110`) |
| `FORGE_DATABASE_URL` | local Postgres DSN | Shared `forge` DB |
| `FORGE_DATABASE_SCHEMA` | `network` | Schema name |
| `FORGE_NETWORK_CLUSTER_CIDR` | `10.100.0.0/16` | Default plan CIDR |
| `FORGE_NETWORK_NODE_PREFIX_LEN` | `24` | Per-node block size |
| `FORGE_NETWORK_PROVIDER_CIDRS` | empty | Comma-separated install-target private CIDRs |
| `FORGE_NETWORK_LEASE_RECLAIM_INTERVAL_S` | `60` | Orphan workload lease sweep |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Bridge subnet collision source |
| `FORGE_NETWORK_WG_MTU` | `1420` | Advertised MTU in peer sets |
| `FORGE_NETWORK_WG_KEEPALIVE_S` | `25` | `persistent_keepalive` for NAT'd peers |
| `FORGE_NETWORK_WG_TOPOLOGY` | `mesh` | `hub` documented only ([hub-topology.md](../../docs/implementation/steps/22-forge-network/notes/hub-topology.md)) |
| `FORGE_NETWORK_WG_ROTATION_WINDOW_S` | `300` | Dual-key window before scheduled retire |

## WireGuard peers

```bash
# After node lease exists:
curl -s -X PUT localhost:4110/v1/networks/cluster-overlay/nodes/node-a/wireguard \
  -H 'content-type: application/json' \
  -d '{"public_key":"b64:...", "endpoint":"203.0.113.5:51820"}' | jq

curl -s localhost:4110/v1/networks/cluster-overlay/nodes/node-a/peers | jq

curl -s -X POST localhost:4110/v1/networks/cluster-overlay/nodes/node-a/rotate-key \
  -H 'content-type: application/json' \
  -d '{"new_public_key":"b64:rotated..."}' | jq
```

## OpenAPI

[`contracts/openapi/forge-network.openapi.yaml`](../../contracts/openapi/forge-network.openapi.yaml)

## Related

* Epic: [`docs/implementation/epics/22-forge-network.md`](../../docs/implementation/epics/22-forge-network.md)
* Step `22.01`: [`22.01-skeleton-and-address-allocation.md`](../../docs/implementation/steps/22-forge-network/22.01-skeleton-and-address-allocation.md)
* Step `22.03`: [`22.03-wireguard-peer-management.md`](../../docs/implementation/steps/22-forge-network/22.03-wireguard-peer-management.md)
