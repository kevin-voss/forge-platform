# forge-infrastructure

Go service (host port **4111**) that turns declared `NodePool` resources into real machines via pluggable provider adapters.

## Step 23.07 (epic gate)

* Health: `GET /health/live`, `GET /health/ready`
* Debug: `GET /v1/operations/{opId}` (operation ledger)
* Admission: `POST /v1/admission/infrastructureproviders` (inventory + Hetzner/AWS/Azure config schema)
* `Provider` interface (16 methods) + registry
* **`docker` provider** — starts `forge-runtime` containers as independent nodes (Docker socket + Compose DNS address)
* **`ssh` / `bare-metal` providers** — adopt/release from static inventory
* **`hetzner` provider** — Hetzner Cloud IaaS (servers, private networks, volumes, floating IPs)
* **`aws` provider** — EC2 / EBS / VPC / Elastic IP only (no EKS/ECS/RDS/…)
* **`azure` provider** — VM / managed disk / VNet / public IP only (no AKS/App Service/…)
* Finite capacity: `NodePool.status.maxReplicas` + `False/InventoryExhausted` when `replicas` exceeds inventory
* **`NodeController`** — `Provisioning → Bootstrapping → Joining → Ready → Draining → Deleting`
* Bootstrap payload templating (cloud-init + SSH script) + epic-22 bootstrap token client
* `node_bootstrap_timers` deadlines; automatic delete on bootstrap/join timeout
* Drain-before-delete (epic 08 reschedule hook); drain timeout deletes with stranded workload log
* Cluster-scoped kinds: `InfrastructureProvider`, `NodePool`, `Node` (plural **`forgenodes`** — avoids Control fleet `GET /v1/nodes`)
* `provider_operations` ledger (`op_<ULID>`) for idempotent mutating calls
* `ssh_inventory_claims` table for exclusive host claim/release
* Gate demo: `make demo DEMO=23` (`demos/23-local-cloud-simulation`)

## Configuration

| Env | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | In-container listen port (host `4111`) |
| `FORGE_INFRA_DB_URL` / `FORGE_DATABASE_URL` | local Postgres | Ledger + timers + inventory claims DB |
| `FORGE_DATABASE_SCHEMA` | `infrastructure` | Schema for ledger/timers/claims |
| `FORGE_REGISTRY_URL` | `http://forge-control:8080` | Epic-20 resource API |
| `FORGE_INFRA_RECONCILE_INTERVAL_MS` | `2000` | NodePool reconcile period |
| `FORGE_AUTH_MODE` | `dev` | Service-to-service until mTLS |
| `FORGE_INFRA_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker Engine socket (or `DOCKER_HOST`) |
| `FORGE_INFRA_DOCKER_NETWORK` | `forge-platform_default` | Compose network for node containers |
| `FORGE_INFRA_DOCKER_IMAGE` | `forge/forge-runtime:local` | Image started per node |
| `FORGE_INFRA_DOCKER_HOST_ADDRESS` | `127.0.0.1` | Returned by `CreatePublicIP` |
| `FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S` | `30` | Orphan container cleanup period |
| `FORGE_INFRA_SSH_CONNECT_TIMEOUT_SECONDS` | `10` | SSH dial timeout for ssh/bare-metal |
| `FORGE_INFRA_SSH_PROBE_INTERVAL_SECONDS` | `60` | Periodic ValidateCredentials sweep |
| `FORGE_INFRA_HETZNER_API_BASE` | `https://api.hetzner.cloud/v1` | Hetzner Cloud API base |
| `FORGE_INFRA_HETZNER_MAX_CONCURRENT_OPS` | `5` | In-flight Hetzner API call cap |
| `FORGE_INFRA_HETZNER_ORPHAN_SCAN_INTERVAL_S` | `300` | Hetzner orphan sweep period |
| `FORGE_INFRA_HETZNER_API_TOKEN` | _(unset)_ | Optional local-demo token fallback |
| `FORGE_INFRA_AWS_MAX_CONCURRENT_OPS` | `5` | In-flight AWS API call cap |
| `FORGE_INFRA_AWS_ORPHAN_SCAN_INTERVAL_S` | `300` | AWS orphan sweep period |
| `FORGE_INFRA_AWS_API_BASE` | _(unset)_ | Optional EC2 endpoint override / fixture shim |
| `FORGE_INFRA_AWS_CREDENTIALS_JSON` | _(unset)_ | Optional local-demo AWS credentials JSON |
| `FORGE_INFRA_AZURE_MAX_CONCURRENT_OPS` | `5` | In-flight Azure ARM call cap |
| `FORGE_INFRA_AZURE_ORPHAN_SCAN_INTERVAL_S` | `300` | Azure orphan sweep period |
| `FORGE_INFRA_AZURE_ARM_BASE` | `https://management.azure.com` | Azure ARM base URL |
| `FORGE_INFRA_AZURE_CREDENTIALS_JSON` | _(unset)_ | Optional local-demo Azure SP credentials JSON |
| `FORGE_SECRETS_URL` | _(unset)_ | Forge Secrets base for `credentialsSecretRef` |
| `FORGE_CONTROL_URL` | `http://forge-control:8080` | Injected into node containers; fleet/join observe |
| `FORGE_NODE_PROVISION_TIMEOUT_SECONDS` | `180` | Provisioning deadline |
| `FORGE_NODE_BOOTSTRAP_TIMEOUT_SECONDS` | `600` | Bootstrapping deadline |
| `FORGE_NODE_JOIN_TIMEOUT_SECONDS` | `120` | Joining deadline |
| `FORGE_NODE_DRAIN_TIMEOUT_SECONDS` | `300` | Drain-before-delete deadline |
| `FORGE_BOOTSTRAP_TOKEN_URL` | Control URL | epic-22 token issuance (`POST /v1/nodes/bootstrap-tokens`) |
| `FORGE_BOOTSTRAP_ORGANIZATION` | `forge` | Token scope organization |
| `FORGE_EVENTS_URL` | _(unset)_ | Optional `resource.node.phasechanged` publish |

### Local machine types (docker)

| Type | CPU | Memory | Slots |
|---|---:|---:|---:|
| `docker-small` | 1 | 1024 MiB | 2 |
| `docker-medium` | 2 | 2048 MiB | 4 |
| `docker-large` | 4 | 4096 MiB | 8 |

### SSH / bare-metal inventory

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: rack1-baremetal }
spec:
  type: bare-metal   # or ssh
  config:
    inventory:
      - { address: 10.0.4.11, sshUser: forge, sshKeySecretRef: { name: rack1-ssh-key } }
```

SSH keys are always resolved via `sshKeySecretRef` (never inline). Unsupported mutating methods (`CreateNetwork`, disks, public IPs) return typed `ErrNotSupported`.

### Hetzner Cloud

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: hetzner-prod }
spec:
  type: hetzner
  credentialsSecretRef: { name: hetzner-prod-token }
  defaultRegion: fsn1
  config: { networkCIDR: "10.1.0.0/16", orphanGraceMinutes: 15 }
```

* `CreateNode` is idempotent via `GET /servers?label_selector=forge.op_id==<op>` before `POST /servers`
* Rate limits: token-bucket from `RateLimit-*` headers + exponential backoff with jitter on `429`
* `DeleteNode` order: volumes → floating IP → server → (last node in pool) private network
* Orphan sweep deletes `forge.managed=true` resources with no matching `Node` after `orphanGraceMinutes`
* Use one dedicated Hetzner project per Forge organization so a leaked token's blast radius stays bounded

### AWS EC2

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: aws-prod }
spec:
  type: aws
  credentialsSecretRef: { name: aws-prod-credentials }
  defaultRegion: eu-central-1
  config: { vpcCidr: "10.30.0.0/16", orphanGraceMinutes: 15 }
```

* Primitives only: EC2 instances, EBS, VPC/subnet/SG, Elastic IP — see [`docs/operations/aws-provider-permissions.md`](../../docs/operations/aws-provider-permissions.md)
* Idempotency: tag `forge.op_id` + `ClientToken` on `RunInstances`
* `DeleteNode` order: volumes → EIP → instance → (last node) VPC

### Azure VMs

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: azure-prod }
spec:
  type: azure
  credentialsSecretRef: { name: azure-prod-credentials }
  defaultRegion: westeurope
  config: { vnetCidr: "10.40.0.0/16", orphanGraceMinutes: 15 }
```

* Primitives only: VM, managed disk, VNet/NSG, public IP — see [`docs/operations/azure-provider-permissions.md`](../../docs/operations/azure-provider-permissions.md)
* Idempotency: tag `forge.op_id` before `CreateVM`
* `DeleteNode` order: disks → public IP → VM → (last node) VNet

## Local commands

```bash
make -C services/forge-infrastructure test-unit
make -C services/forge-infrastructure run
curl -sf http://127.0.0.1:4111/health/ready
```

## Docker socket

Compose mounts `/var/run/docker.sock` into `forge-infrastructure` (same privileged local-dev tradeoff as `forge-runtime`). The `docker` provider creates/stops labeled containers and named volumes; cloud credentials are never required for this path.
