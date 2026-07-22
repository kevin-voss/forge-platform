# Resource model

**Status:** Target model, introduced by epic `20`. Shipped epics `00`–`19` keep their
current APIs; epic `20` puts this envelope underneath them without changing them.

Every managed platform capability is a **declarative resource**. One shape, one API
style, one lifecycle, one controller pattern — for applications, nodes, databases,
queues, buckets, certificates, and everything else.

---

## 1. Envelope

```yaml
apiVersion: forge.dev/v1
kind: Application

metadata:
  id: app_01HZX9M3QK7T4B2N        # immutable, server-assigned, prefixed ULID
  name: invoice-api               # human name, unique within its scope
  organization: forge-labs
  project: invoice-platform
  environment: production
  generation: 7                   # increments only when spec changes
  resourceVersion: "4821"         # optimistic-concurrency token
  labels:
    tier: backend
  annotations:
    forge.dev/last-applied-by: kevin
  ownerRefs: []
  finalizers: []
  createdAt: 2026-07-22T09:00:00Z
  deletionTimestamp: null

spec: {}                          # desired state — written by users and owners

status:                           # observed state — written only by the controller
  phase: Ready
  observedGeneration: 7
  conditions: []
```

| Field | Meaning |
|---|---|
| `metadata.id` | immutable identity; survives renames; used in audit and events |
| `metadata.name` | human-readable, unique per scope, may be reused after deletion |
| `metadata.generation` | bumps **only** on a `spec` change — status writes never bump it |
| `metadata.resourceVersion` | concurrency token; a stale write gets `409 Conflict` |
| `spec` | what should be true |
| `status` | what is true, as last observed |

`observedGeneration < generation` means "the controller has not caught up yet". That
single comparison is how the CLI, Console, and `forge wait` know whether they are looking
at the result of the change they just made.

---

## 2. Scopes

| Scope | Path prefix | Kinds |
|---|---|---|
| Environment | `/v1/projects/{project}/environments/{environment}/{plural}` | Application, Worker, Agent, CronJob, Service, Route, Deployment, Revision, Database, Queue, Bucket, Volume, Secret, Config, ScalingPolicy, NetworkPolicy, Workflow, MemoryCollection, Backup |
| Project | `/v1/projects/{project}/{plural}` | Environment, ServiceAccount, Domain, Certificate |
| Cluster | `/v1/{plural}` | Organization, Project, Node, NodePool, Region, InfrastructureProvider, Policy, Model, Alert, Incident, BackupPolicy |

Isolation follows the same nesting on every axis — API access, database credentials,
queues, buckets, network policy, logs, metrics, traces, secrets, builds, images:

```text
Organization → Project → Environment → Resource
```

---

## 3. Phases and conditions

`status.phase` is a coarse summary for humans:

```text
Pending → Progressing → Ready
                ↓
            Degraded / Failed
                ↓
            Terminating
```

`status.conditions` carry the machine-readable truth. Conditions are additive; a
controller sets its own condition types and never clears another controller's.

```yaml
status:
  phase: Progressing
  observedGeneration: 4
  conditions:
    - type: Scheduled
      status: "True"
      reason: PlacedOnNodes
      lastTransitionTime: 2026-07-22T10:04:11Z
    - type: Available
      status: "False"
      reason: MinimumReplicasUnavailable
      message: 1/2 replicas ready
      lastTransitionTime: 2026-07-22T10:04:09Z
    - type: Progressing
      status: "True"
      reason: RollingUpdateInProgress
      lastTransitionTime: 2026-07-22T10:04:02Z
```

`status` values are `"True"`, `"False"`, or `"Unknown"` (quoted strings, not booleans, so
"unknown" is representable and future values stay additive).

---

## 4. API surface

```http
POST   /v1/projects/{project}/environments/{env}/applications
GET    /v1/projects/{project}/environments/{env}/applications?labelSelector=tier%3Dbackend
GET    /v1/projects/{project}/environments/{env}/applications/{name}
PUT    /v1/projects/{project}/environments/{env}/applications/{name}
PATCH  /v1/projects/{project}/environments/{env}/applications/{name}
PUT    /v1/projects/{project}/environments/{env}/applications/{name}/status
DELETE /v1/projects/{project}/environments/{env}/applications/{name}
GET    /v1/watch/applications?since=4821
```

Conventions:

* **Optimistic concurrency** — send `metadata.resourceVersion`; mismatch returns `409` with
  the canonical error envelope from epic `02`.
* **Status subresource** — only the owning controller may write `/status`; user writes to
  `spec` cannot touch `status` and vice versa.
* **Watch** — server-sent events with `ADDED` / `MODIFIED` / `DELETED` frames, resumable
  from a `resourceVersion`; `410 Gone` when the cursor has aged out of the replay buffer.
* **List** — label selectors (`=`, `!=`, `in`, `notin`, existence), field filters, stable
  ordering, cursor pagination, and a list-level `resourceVersion` a watch can start from.
* **Idempotency** — `Idempotency-Key` on creates behaves exactly as the shipped Control API
  already defines.

---

## 5. Controller ownership

Exactly one primary controller per kind.

| Resource | Primary controller |
|---|---|
| Application | Deployment controller |
| Deployment / Revision | Reconciliation controller |
| Node | Node controller |
| NodePool | Node autoscaler |
| ScalingPolicy | Autoscaler |
| Database / DatabaseReplica | Database controller |
| Queue / Topic / Subscription | Queue controller |
| Bucket / Object | Storage controller |
| Volume / Snapshot | Volume controller |
| Certificate | Certificate controller |
| Domain | DNS controller |
| NetworkPolicy | Network controller |
| Backup / Restore | Backup controller |
| Incident | Incident controller |
| Model | Model controller |

Every controller must:

1. read desired state
2. inspect actual state
3. perform **idempotent** actions
4. update `status` (and only its own resource's status)
5. retry temporary failures with backoff
6. expose permanent failures as a `False` condition with a `reason`
7. survive process restarts by recomputing from persisted state
8. avoid duplicate operations

### The idempotency rule

Every externally visible action carries an operation id.

```text
Wrong:     create VM
Required:  create VM for infrastructure-operation op_01HZX…
```

Executing `op_01HZX…` twice returns the same VM or the same terminal result. The operation
record is written *before* the external call, so a crash between call and response is
recoverable.

---

## 6. Ownership, finalizers, deletion

```yaml
metadata:
  ownerRefs:
    - kind: Application
      id: app_01HZX9M3QK7T4B2N
      controller: true
  finalizers:
    - forge.dev/volume-detach
    - forge.dev/backup-retention
```

* An owner reference makes cleanup automatic for **stateless** children (deployments,
  revisions, routes, endpoints).
* A finalizer blocks hard deletion until the responsible controller has finished its
  cleanup and removed its own entry.
* `DELETE` sets `deletionTimestamp` and `phase: Terminating`; the row disappears only when
  the finalizer list is empty.
* **Stateful kinds — Database, Volume, Bucket, Backup — are never removed by cascade.**
  They require explicit confirmation and honour deletion protection and retention policy.

---

## 7. Events

Every mutation emits a durable platform event:

```json
{
  "event_id": "evt_01HZX9…",
  "type": "resource.application.updated",
  "resource_id": "app_01HZX9M3QK7T4B2N",
  "resource_generation": 7,
  "occurred_at": "2026-07-22T10:04:02Z",
  "producer": "forge-control",
  "schema_version": "1",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "idempotency_key": "op_01HZX9…"
}
```

Consumers (autoscaler, gateway, console, workflows, alerts) subscribe rather than poll.
Events are informational: a controller that misses an event must still converge from
resource state alone.

---

## 8. Audit

Every control-plane mutation records actor, service account, action, resource kind,
resource id, previous generation, new generation, timestamp, request id, source IP where
applicable, result, and failure reason. Audit is written in the same transaction as the
mutation — an action that is not auditable does not happen.

---

## 9. Resource kinds (target set)

```text
Organization  Project      Environment   Application   Worker      Agent
CronJob       Service      Route         Deployment    Revision    Node
NodePool      Region       ScalingPolicy Database      DatabaseReplica
Queue         Topic        Subscription  Bucket        Object      Volume
Snapshot      Secret       Config        Certificate   Domain      NetworkPolicy
ServiceAccount Workflow    Model         MemoryCollection  Backup   BackupPolicy
Restore       Policy       Alert         Incident      InfrastructureProvider
```

Kinds are added by epics; the envelope, the API conventions, and the controller contract
never change.
