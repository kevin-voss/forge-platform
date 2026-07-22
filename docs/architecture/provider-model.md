# Infrastructure provider model

**Status:** Target model, introduced by epic `23` (Forge Infrastructure) and extended by
epic `43` (provider plugins).

Forge treats every infrastructure provider — including a laptop — as a source of the same
handful of primitives. Everything above that line is Forge's own behaviour.

---

## 1. What a provider is allowed to supply

```text
virtual machines · CPU · memory · disks · private networks · public IP addresses ·
DNS delegation · GPU machines
```

That is the complete list. A provider adapter that needs more than this is doing something
Forge should be doing itself.

### What Forge never requires

| Never required | Forge equivalent |
|---|---|
| AWS ECS / EKS, Azure App Service | Forge Runtime + Scheduler |
| AWS RDS, Azure Database, Hetzner managed DB | Forge Database controller (epics `18`, `29`) |
| AWS SQS, Azure Service Bus | Forge Events + Forge Queue (epics `11`, `28`) |
| AWS Lambda | Forge Workers / CronJobs |
| AWS CloudWatch, Azure Monitor | Forge Observe (epic `12`) |
| Provider load balancers | Forge Gateway (epic `05`) |
| Provider secret managers | Forge Secrets (epics `10`, `32`) |

Managed services may be added later as **optional adapters** a platform operator opts into.
Nothing in the default architecture may depend on one.

---

## 2. Provider adapter interface

```text
validateCredentials()
listRegions()
listMachineTypes()

createNetwork()        deleteNetwork()
createNode()           deleteNode()        rebootNode()
getNode()              listNodes()
attachDisk()           detachDisk()        resizeDisk()
createPublicIP()       deletePublicIP()
getPricing()
```

Rules for every adapter:

1. **Operation ids.** Each mutating call receives an `op_…` id, recorded before the call.
   Repeating the id returns the same resource or the same terminal outcome — never a
   second machine.
2. **Tagged inventory.** Everything Forge creates is tagged/labelled with the installation
   id and resource id, so a reconciliation pass can find and remove anything Forge created
   but lost track of. This is the defence against paying for orphans.
3. **No hidden state.** Adapter state lives in Forge resources, not in adapter memory.
4. **Credentials from Forge Secrets.** Never from a manifest, never from environment
   variables baked into an image.
5. **Failure honesty.** Quota exhaustion, throttling, and eventual consistency surface as
   typed conditions on the `Node`, not as retries forever.

---

## 3. Required adapters

| Adapter | Role |
|---|---|
| local process | run everything in one process — the smallest possible development loop |
| **Docker** | nodes are containers on one machine; the CI and demo target for every epic |
| generic SSH | turn any reachable host into a Forge node |
| bare-metal static | machines are declared, not created; `create`/`delete` become adopt/release |
| Hetzner Cloud | servers, private networks, volumes, primary IPs |
| AWS EC2 | instances, EBS volumes, VPC/subnets, elastic IPs |
| Azure VM | virtual machines, managed disks, VNets, public IPs |

The Docker adapter is not a toy: it is how "local cloud simulation" works, and every
scheduling, autoscaling, network, and failure demo runs on it.

```text
NodePool provider = docker
→ Forge Infrastructure starts runtime-node containers
→ each container registers as an independent node
→ the scheduler treats them exactly like remote machines
```

---

## 4. Node pools

```yaml
apiVersion: forge.dev/v1
kind: NodePool

metadata:
  name: general-purpose

spec:
  provider: aws
  region: eu-central-1
  machine:
    cpu: 4
    memory: 16Gi
    architecture: amd64
  scaling:
    minNodes: 2
    maxNodes: 20
  labels:
    workload-class: general
```

A node pool is the **only** place provider-specific vocabulary appears, and it is owned by
the platform operator. Product manifests reference capability (`workload-class: general`,
`gpu: 1`), never a pool or a provider.

Multiple pools may coexist across providers in one installation — that is what makes
cross-provider clusters (epic `39`) a configuration change rather than a migration.

---

## 5. Node lifecycle

```text
Provisioning → Bootstrapping → Joining → Ready → Draining → Deleting
```

```text
Node autoscaler requests one node
→ Infrastructure selects the provider adapter
→ adapter creates the machine (operation id first, then the call)
→ cloud-init / SSH installs Forge Runtime
→ Runtime receives a bootstrap token and joins the Forge network
→ Runtime registers with Control
→ node health checks pass
→ node becomes schedulable
```

and back down:

```text
Node marked Draining
→ scheduler stops placing new workloads
→ existing workloads move, honouring disruption budgets
→ node is empty
→ Infrastructure deletes the machine and releases its address, disks, and IPs
```

Bootstrap failures time out and clean up: a machine that never reaches `Ready` within its
deadline is deleted, so a broken image or a bad credential cannot quietly accumulate cost.

---

## 6. Installation targets

```bash
forge install --target docker                       # laptop, CI
forge install --target ssh --inventory hosts.yaml   # existing machines
forge install --target bare-metal --inventory rack.yaml
forge install --target hetzner --region nbg1
forge install --target aws --region eu-central-1
forge install --target azure --region westeurope
```

Installing sets up the control plane, the first node pool, and the network mode. Everything
above — projects, applications, databases, queues, scaling — is identical afterwards.

---

## 7. Cost awareness

`getPricing()` feeds the usage and cost epic (`41`). When more than one eligible pool can
satisfy demand and policy permits both, the cheaper permitted option wins:

```text
Autoscaler requires 16 additional CPU cores
→ Infrastructure compares eligible providers
→ policy allows AWS and Hetzner
→ price adapter evaluates current price
→ capacity is created on the permitted lower-cost provider
```

Cost limits are a hard safeguard, not a recommendation: a pool that would exceed its
budget ceiling refuses to grow and reports it as a condition.
