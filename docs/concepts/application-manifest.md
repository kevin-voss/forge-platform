# Application manifest and deployment targets

**Status:** Target UX, introduced by epic `20` (`forge apply`) and completed by epics
`23`–`25`. Today's `forge.yaml` build manifest (epic `06`) keeps working unchanged.
Proven end-to-end by [`demos/20-declarative-resources`](../../demos/20-declarative-resources)
(`make demo DEMO=20`).

This is the customer-facing contract: **what a product team writes**, and **what they
never have to write**. The portable `Application` envelope below is the **recommended
future entry point** for deploying product workloads (`forge apply -f`).

---

## 1. One manifest

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
    database:
      type: postgres
      plan: standard
    queue:
      type: durable
      name: invoice-jobs
    storage:
      type: object
      bucket: invoices
```

One file may hold several documents — an `Application`, its `Worker`, a `Queue`, a
`Route`, a `ScalingPolicy` — separated by `---`. `forge apply` submits them as a set.

```bash
forge apply -f forge.yaml
# Generic resource API (also exercised by demos/20-declarative-resources):
#   GET /v1/projects/{project}/environments/{env}/applications/{name}
#   GET /v1/watch/applications?since={resourceVersion}
forge get applications
forge describe application invoice-api
forge wait application/invoice-api --for=condition=Ready --timeout=5m
forge delete -f forge.yaml
```

---

## 2. Portability rules

The manifest describes **the product**. It never describes **the infrastructure**.

| A product manifest may contain | A product manifest must never contain |
|---|---|
| image reference and version | provider names (`aws`, `hetzner`, `azure`) |
| CPU / memory / GPU requests | machine types (`t3.large`, `cx41`) |
| replica bounds and scaling policies | region or zone ids |
| dependency *types* (`postgres`, `durable`, `object`) | IP addresses, CIDRs, security groups |
| logical names (`invoice-jobs`, `invoices`) | disk types (`gp3`, `Premium_LRS`) |
| routes, health paths, env var *names* | managed-service names (RDS, SQS, Service Bus) |
| placement *intent* (spread, minimum distinct nodes) | node names, credentials, connection strings |

Infrastructure detail lives in operator-owned resources — `InfrastructureProvider`,
`NodePool`, `Region`, `Policy` — which a product team never edits. That separation is what
makes the same manifest deployable everywhere.

### Dependencies are typed, not branded

```yaml
dependencies:
  database: { type: postgres, plan: standard }
```

Forge resolves this to a `Database` resource managed by the Forge database controller —
the same PostgreSQL topology on a laptop and on a 40-node AWS fleet. It never resolves to
RDS or Azure Database unless a platform operator has explicitly installed and permitted an
optional managed adapter.

---

## 3. Deployment targets

```bash
forge deploy --target local
forge deploy --target hetzner
forge deploy --target aws
forge deploy --target azure
forge deploy --target bare-metal
```

A **target** is where a Forge installation runs, not a property of the application. It is
selected once by the operator when installing Forge:

```bash
forge install --target docker            # laptop / CI: nodes are containers
forge install --target ssh --inventory hosts.yaml
forge install --target hetzner --region nbg1
forge install --target aws --region eu-central-1
forge install --target azure --region westeurope
```

Installation creates operator-owned resources:

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

Swap `provider: aws` for `provider: hetzner` or `provider: docker` and every product
manifest in the installation keeps working.

---

## 4. Scaling is declared, not operated

```yaml
scaling:
  minReplicas: 2
  maxReplicas: 20
  policies:
    - { type: cpu, targetAverageUtilization: 65 }
    - { type: httpRequests, targetRequestsPerSecond: 150 }
    - { type: queueDepth, queue: invoice-jobs, targetPerReplica: 500 }
  schedules:
    - { cron: "0 7 * * MON-FRI", minReplicas: 10 }
    - { cron: "0 20 * * *",      minReplicas: 2 }
```

Node capacity follows automatically: if the cluster cannot place the replicas, the node
autoscaler asks Infrastructure for machines; when demand falls, nodes drain and are
deleted. The product team never sizes a cluster. See
[autoscaling-model.md](autoscaling-model.md).

---

## 5. Placement intent

Availability is expressed as intent, never as node names:

```yaml
placement:
  nodeSelector:
    workload-class: general
  antiAffinity:
    spreadAcross: [node, availability-zone]
  minimumDistinctNodes: 2
  regions: [eu-central, eu-west]
  minimumRegions: 2
  dataResidency:
    allowedCountries: [DE, NL, FR]
```

The scheduler translates intent into placements for whatever fleet exists — three
containers on a laptop, or thirty VMs across two providers.

---

## 6. Relationship to today's `forge.yaml`

| File | Today (epic 06) | Target |
|---|---|---|
| `forge.yaml` | build manifest: source, Dockerfile, build args, service name | still valid, unchanged |
| `forge.yaml` with `apiVersion: forge.dev/v1` documents | — | resource documents applied by `forge apply` |

Both forms coexist. A repository can keep its build manifest and add resource documents
beside it; `forge apply` reads resource documents, `forge build` reads the build manifest.
Nothing shipped in epic `06` is invalidated.

---

## 7. What a customer does, end to end

```bash
forge login                                   # identity (epic 09)
forge project create invoice-platform         # scope
forge build --source .                        # image → Forge Registry (epics 06, 26)
forge apply -f forge.yaml                     # Application + Queue + Route
forge wait application/invoice-api --for=condition=Ready
forge get applications                        # phase, replicas, endpoints
forge logs invoice-api --follow               # observe (epic 12)
```

Nothing in that sequence changes when the installation moves from a laptop to Hetzner, to
AWS, or to a mixed fleet.
