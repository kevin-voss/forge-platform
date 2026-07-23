# forge-network

Provider-independent cluster overlay address plan (epic 22). Host port `4110` /
container `8080`.

Step `22.01` stands up the service skeleton, Postgres-backed `Network` resources,
and node/workload address leases carved from a cluster CIDR (default
`10.100.0.0/16` → one `/24` per node → one address per workload).

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

## OpenAPI

[`contracts/openapi/forge-network.openapi.yaml`](../../contracts/openapi/forge-network.openapi.yaml)

## Related

* Epic: [`docs/implementation/epics/22-forge-network.md`](../../docs/implementation/epics/22-forge-network.md)
* Step: [`docs/implementation/steps/22-forge-network/22.01-skeleton-and-address-allocation.md`](../../docs/implementation/steps/22-forge-network/22.01-skeleton-and-address-allocation.md)
