# Target architecture — Forge as a standalone cloud

**Status:** Specification for epics `20`–`43`. Nothing here changes epics `00`–`19`, which
are in flight. This document is the source of truth for what Forge becomes *after* the
current roadmap completes.

Related: [overview.md](overview.md) (shipped shape) ·
[provider-model.md](provider-model.md) · [networking-and-discovery.md](networking-and-discovery.md) ·
[resource model](../concepts/resource-model.md) ·
[application manifest](../concepts/application-manifest.md) ·
[autoscaling model](../concepts/autoscaling-model.md) ·
[future plan](../implementation/FUTURE_PLAN.md).

---

## 1. Purpose

Today Forge is a locally runnable developer platform. The target is a **standalone,
horizontally scalable application platform** that behaves identically on:

* local Docker
* local virtual machines
* bare-metal servers
* Hetzner Cloud
* AWS EC2
* Azure Virtual Machines
* any other infrastructure provider with a small adapter

**Forge owns the platform behaviour.** Infrastructure providers supply primitives only:

| Providers supply | Forge supplies |
|---|---|
| virtual machines, CPU, memory | scheduling, autoscaling, deployment |
| disks, private networks, public IPs | volumes, overlay network, service discovery |
| DNS delegation, GPU machines | DNS, certificates, databases, queues, storage, models |

### Non-negotiable

No provider-managed service may ever be **required**: not ECS, EKS, RDS, SQS, Lambda or
CloudWatch; not Azure App Service, Service Bus or Database for PostgreSQL; not Hetzner
managed databases. Managed services may appear later as *optional adapters* that a
platform operator opts into — never as a dependency of the default architecture.

The reason is the product promise: a customer who deploys on Hetzner and a customer who
deploys on AWS run the *same platform*, not two platforms that happen to share a CLI.

---

## 2. North star

A product developer defines an application once:

```yaml
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: invoice-api
  project: invoice-platform
  environment: production
spec:
  image: registry.forge.internal/forge-labs/invoice-platform/invoice-api:1.4.0
  resources:
    cpu: 1000m
    memory: 1024Mi
  scaling:
    minReplicas: 2
    maxReplicas: 20
    policies:
      - { type: cpu, targetAverageUtilization: 65 }
      - { type: httpRequests, targetRequestsPerSecond: 150 }
  dependencies:
    database: { type: postgres, plan: standard }
    queue:    { type: durable, name: invoice-jobs }
    storage:  { type: object, bucket: invoices }
```

and ships it anywhere:

```bash
forge deploy --target local
forge deploy --target hetzner
forge deploy --target aws
forge deploy --target azure
forge deploy --target bare-metal
```

Only the **installation target** changes. The manifest never contains provider names,
machine types, region ids, IP addresses, disk types, security groups, or managed-service
names — see [application-manifest.md](../concepts/application-manifest.md) for the full
portability rules.

---

## 3. Architectural adjustments

The shipped architecture is the foundation. Three changes make it a cloud.

### 3.1 Separate workload scheduling from infrastructure provisioning

```text
Forge Autoscaler        how many replicas / how many nodes?
        ↓
Forge Infrastructure    where do machines come from?
        ↓
Provider Adapter        Docker · SSH · bare metal · Hetzner · AWS · Azure
        ↓
Forge Runtime Nodes     how are workloads started on a node?
```

Responsibilities never blur:

| Component | Answers exactly one question |
|---|---|
| Scheduler | Where should this workload run? |
| Workload Autoscaler | How many workload replicas should exist? |
| Node Autoscaler | How many runtime nodes should exist? |
| Infrastructure | How are nodes created and deleted? |
| Runtime | How are workloads started on a node? |

The scheduler must never create a virtual machine. The autoscaler must never start a
container. Every crossing goes through a resource, so it is observable and replayable.

### 3.2 One declarative resource model

Every managed capability becomes a resource with `apiVersion`, `kind`, `metadata`,
`spec`, and `status` — desired state in `spec`, observed state in `status`, immutable
identity in `metadata.id`, human name in `metadata.name`, change tracking in
`metadata.generation`. Full definition: [resource-model.md](../concepts/resource-model.md).

### 3.3 One controller per resource kind

Each kind has exactly one primary controller that reads desired state, inspects actual
state, acts idempotently, updates status, retries transient failures, surfaces permanent
failures as conditions, survives restarts, and never performs an operation twice.

---

## 4. Service topology

The long-term component set:

```text
forge-control      forge-network      forge-registry     forge-observe      forge-policy
forge-runtime      forge-identity     forge-deploy       forge-alerts       forge-dns
forge-scheduler    forge-secrets      forge-events       forge-backup       forge-certificates
forge-autoscaler   forge-build        forge-queue        forge-workflows    forge-billing
forge-infrastructure  forge-gateway   forge-data         forge-models       forge-console
forge-discovery                       forge-storage      forge-agents
                                      forge-volumes      forge-memory
```

**Components are not processes.** Split a service out only when operational or ownership
complexity justifies it. Initial grouping:

```text
forge-control            forge-autoscaler         forge-infrastructure
├── resource API         ├── workload autoscaling ├── local / Docker provider
├── reconciliation       ├── worker autoscaling   ├── SSH / bare-metal provider
├── deployment control   ├── node autoscaling     ├── Hetzner provider
├── audit                └── scheduled scaling    ├── AWS provider
└── status aggregation                            └── Azure provider

forge-data               forge-network
├── PostgreSQL controller├── node overlay network
├── queue controller     ├── service networking
├── volume controller    ├── network policy
├── backup controller    └── internal DNS integration
└── restore controller
```

Ports are reserved in [ports.md](../operations/ports.md).

---

## 5. Layered runtime picture

```text
                        Forge CLI / Console
                                ↓
                         Forge Gateway API
                                ↓
                          Forge Control            ← declarative resource API
                                ↓
   ┌────────────┬──────────────┼──────────────┬──────────────────┐
   ↓            ↓              ↓              ↓                  ↓
Reconciler  Scheduler     Autoscaler       Policy         Infrastructure
                                ↓                                ↓
                        desired capacity                 provider adapters
                                                                 ↓
                                          Docker · bare metal · Hetzner · AWS · Azure
                                                                 ↓
                                                      Forge Runtime nodes
                                                                 ↓
                   ┌─────────────────────────────────────────────┼──────────┐
                   ↓                                             ↓          ↓
             Applications                                     Workers     Agents
                   ↓                                             ↓          ↓
              Gateway + Discovery + Forge Network
                   ↓
   ┌───────────────┼───────────────┬────────────────────┐
   ↓               ↓               ↓                    ↓
Database         Queue          Storage              Models
   ↓               ↓               ↓                    ↓
Volumes         Events          Backups              Memory
                   ↓
            Observe + Alerts
                   ↓
          Incidents + Workflows
```

---

## 6. Milestones

| Milestone | Epics | Forge can then… |
|---|---|---|
| **M1 — Standalone cloud core** | `20`–`25` | manage declarative resources, discover services, connect multiple nodes over its own network, create infrastructure on any provider, autoscale workloads and nodes, recover workloads after node failure — the same manifest on Docker, Hetzner, AWS, Azure |
| **M2 — Production platform** | `26`–`37` | run production deployments (canary/blue-green), durable product queues, highly available databases, persistent volumes, distributed object storage, HA secrets, policy admission, TLS + DNS, an HA control plane, platform-wide backups, and incident-driven rollback |
| **M3 — Global platform** | `38`–`43` | schedule GPUs and models, span regions and providers, operate through a console, account for usage and cost, upgrade itself safely, and be extended by signed plugins |

Milestone exit gates and the full epic list live in
[`FUTURE_PLAN.md`](../implementation/FUTURE_PLAN.md).

---

## 7. Cross-cutting requirements

### Idempotency

Every mutating operation carries an **operation id**. Retrying `op_123` must return the
same result — the same VM, the same backup, the same certificate — never a duplicate.
Applies to builds, deployments, node creation, failover, backup, restore, and certificate
issuance.

### Auditability

Every control-plane mutation records: actor, service account, action, resource kind,
resource id, previous generation, new generation, timestamp, request id, source IP where
applicable, result, and failure reason.

### Security

Mutual TLS between platform services · node certificates · workload identities ·
short-lived tokens · least-privilege permissions · encrypted secrets · encrypted backups ·
encrypted network traffic · image signing · audit logging · admission policies · regular
key rotation · **no shared global administrator token**.

### Multi-tenancy

```text
Organization
└── Project
    └── Environment
        └── Resource
```

Isolation is enforced on every axis: APIs, database credentials, queues, buckets, network
policy, logs, metrics, traces, agents, models, secrets, builds, and container images.

### Reliability

Every service defines: health, readiness, and liveness endpoints; startup behaviour; retry
and timeout behaviour; graceful shutdown; durability expectations; backup expectations;
high-availability mode; and a written recovery procedure.

### Deletion

Deletion uses finalizers. No stateful resource is ever silently removed by a cascade:

```text
User deletes Project
→ Project enters Terminating
→ applications removed
→ routes removed
→ databases require explicit confirmation
→ buckets follow retention policy
→ backups follow retention policy
→ secrets revoked
→ finalizers removed
→ Project disappears
```

### Event-driven control plane

Controllers communicate through durable platform events where it reduces coupling:

```text
resource.application.updated   node.registered              database.failover.started
deployment.created             node.unreachable             database.failover.completed
deployment.ready               workload.pending             backup.completed
deployment.failed              autoscaling.recommendation.created   incident.created
                                                            certificate.expiring
```

Every event carries: event id, resource id, resource generation, timestamp, producer,
schema version, trace id, and idempotency key.

---

## 8. Compatibility strategy

This is the rule that keeps the in-flight roadmap safe.

1. **Nothing shipped is rewritten.** Epic `20` introduces the generic resource API *inside*
   Forge Control; the existing `/v1/projects/...` endpoints become a thin facade over the
   same rows and keep their OpenAPI contract and tests.
2. **New capability = new resource kind or new optional field.** Existing fields keep their
   meaning and defaults.
3. **New services are additive.** Discovery, Network, Infrastructure, and Autoscaler start
   disabled or in pass-through mode; each one flips behind a flag only after parity with
   the shipped behaviour is demonstrated, and each flag is reversible.
4. **Local Docker is the CI target.** Cloud-target demos are opt-in
   (`FORGE_DEMO_TARGET=hetzner|aws|azure`) and never gate a merge.
5. **Step numbering is append-only.** Future work starts at `N = 132`; steps `1`–`131` are
   frozen.

---

## 9. The promise

```text
One application manifest      One database API
One deployment API            One queue API
One identity model            One storage API
One networking model          One observability system
One autoscaling model         One AI runtime
                              One operational workflow

Across: local Docker · bare metal · Hetzner · AWS · Azure ·
        multiple providers · multiple regions
```

Cloud providers supply infrastructure. **Forge supplies the cloud platform.**
