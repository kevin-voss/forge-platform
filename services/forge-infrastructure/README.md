# forge-infrastructure

Go service (host port **4111**) that turns declared `NodePool` resources into real machines via pluggable provider adapters.

## Step 23.03 (current)

* Health: `GET /health/live`, `GET /health/ready`
* Debug: `GET /v1/operations/{opId}` (operation ledger)
* `Provider` interface (16 methods) + registry
* **`docker` provider** — starts `forge-runtime` containers as independent nodes
* **`NodeController`** — `Provisioning → Bootstrapping → Joining → Ready → Draining → Deleting`
* Bootstrap payload templating (cloud-init + SSH script) + epic-22 bootstrap token client
* `node_bootstrap_timers` deadlines; automatic delete on bootstrap/join timeout
* Drain-before-delete (epic 08 reschedule hook); drain timeout deletes with stranded workload log
* Cluster-scoped kinds: `InfrastructureProvider`, `NodePool`, `Node`
* `provider_operations` ledger (`op_<ULID>`) for idempotent mutating calls

## Configuration

| Env | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | In-container listen port (host `4111`) |
| `FORGE_INFRA_DB_URL` / `FORGE_DATABASE_URL` | local Postgres | Ledger + timers DB |
| `FORGE_DATABASE_SCHEMA` | `infrastructure` | Schema for ledger/timers |
| `FORGE_REGISTRY_URL` | `http://forge-control:8080` | Epic-20 resource API |
| `FORGE_INFRA_RECONCILE_INTERVAL_MS` | `2000` | NodePool reconcile period |
| `FORGE_AUTH_MODE` | `dev` | Service-to-service until mTLS |
| `FORGE_INFRA_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker Engine socket (or `DOCKER_HOST`) |
| `FORGE_INFRA_DOCKER_NETWORK` | `forge-platform_default` | Compose network for node containers |
| `FORGE_INFRA_DOCKER_IMAGE` | `forge/forge-runtime:local` | Image started per node |
| `FORGE_INFRA_DOCKER_HOST_ADDRESS` | `127.0.0.1` | Returned by `CreatePublicIP` |
| `FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S` | `30` | Orphan container cleanup period |
| `FORGE_CONTROL_URL` | `http://forge-control:8080` | Injected into node containers; fleet/join observe |
| `FORGE_NODE_PROVISION_TIMEOUT_SECONDS` | `180` | Provisioning deadline |
| `FORGE_NODE_BOOTSTRAP_TIMEOUT_SECONDS` | `600` | Bootstrapping deadline |
| `FORGE_NODE_JOIN_TIMEOUT_SECONDS` | `120` | Joining deadline |
| `FORGE_NODE_DRAIN_TIMEOUT_SECONDS` | `300` | Drain-before-delete deadline |
| `FORGE_BOOTSTRAP_TOKEN_URL` | Control URL | epic-22 token issuance (`POST /v1/nodes/bootstrap-tokens`) |
| `FORGE_BOOTSTRAP_ORGANIZATION` | `forge` | Token scope organization |
| `FORGE_EVENTS_URL` | _(unset)_ | Optional `resource.node.phasechanged` publish |

### Local machine types

| Type | CPU | Memory | Slots |
|---|---:|---:|---:|
| `docker-small` | 1 | 1024 MiB | 2 |
| `docker-medium` | 2 | 2048 MiB | 4 |
| `docker-large` | 4 | 4096 MiB | 8 |

## Local commands

```bash
make -C services/forge-infrastructure test-unit
make -C services/forge-infrastructure run
curl -sf http://127.0.0.1:4111/health/ready
```

## Docker socket

Compose mounts `/var/run/docker.sock` into `forge-infrastructure` (same privileged local-dev tradeoff as `forge-runtime`). The `docker` provider creates/stops labeled containers and named volumes; cloud credentials are never required for this path.
