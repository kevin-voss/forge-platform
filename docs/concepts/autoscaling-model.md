# Autoscaling model

**Status:** Target model, introduced by epic `24` with scheduling support from epic `25`.

Forge has **one** autoscaling model. The same policy language, the same safeguards, and
the same audit trail cover applications, workers, agents, models, and the machines
underneath them.

---

## 1. Two loops, never merged

```text
Workload autoscaling          Node autoscaling
"how many replicas?"          "how many machines?"
        ↓                             ↓
 Application.spec.scaling       NodePool.status.desiredNodes
   .desiredReplicas             (+ NodePool.spec.replicas)
        ↓                             ↓
 Reconciler → Scheduler         Infrastructure → provider adapter
        ↓                             ↓
 Runtime starts containers      VM boots, Runtime joins, node Ready
```

The autoscaler only ever changes **numbers** on resources. It never starts a container and
never calls a cloud API. That separation makes every scaling event replayable and keeps
the blast radius of a bad metric to "wrong replica count", not "orphaned VMs".

---

## 2. Policy

```yaml
apiVersion: forge.dev/v1
kind: ScalingPolicy

metadata:
  name: invoice-api-autoscaling

spec:
  targetRef:
    kind: Application
    name: invoice-api

  minReplicas: 2
  maxReplicas: 20

  metrics:
    - { type: cpu,          targetAverageUtilization: 65 }
    - { type: httpRequests, targetValue: 150 }

  behavior:
    scaleUp:
      stabilizationWindowSeconds: 30
      maxReplicasPerMinute: 5
    scaleDown:
      stabilizationWindowSeconds: 300
      maxReplicasPerMinute: 2

  schedules:
    - { cron: "0 7 * * MON-FRI", minReplicas: 10 }
    - { cron: "0 20 * * *",      minReplicas: 2 }

  metricOutageFallback:
    mode: hold   # hold | floor | fixed
```

Scaling can also be written inline in an `Application` (see
[application-manifest.md](application-manifest.md)); Forge materializes it into a
`ScalingPolicy` owned by that application.

---

## 3. Signals by workload class

| Class | Signals |
|---|---|
| HTTP / gRPC / WebSocket applications | CPU, memory, requests per second, active connections, p95 latency, error rate, custom metrics |
| Workers | queue depth, oldest-message age, average processing duration, consumer lag, retry rate |
| Agents | pending agent runs, model queue duration, token throughput, tool-execution backlog, GPU utilization |
| Models | request concurrency, batch fill, tokens/second, warm-pool occupancy, scale-to-zero idle time |
| Nodes | pending unschedulable workloads, cluster CPU/memory reservation, GPU capacity, node utilization, availability requirements |

All signals arrive through one metric-source interface, so a new source is an adapter, not
a new autoscaler.

---

## 4. Flows

### Workload scale-up

```text
Observe publishes metrics
→ Autoscaler computes desired replicas
→ ScalingPolicy status updated (inputs + decision recorded)
→ Application desired replicas change
→ Reconciler creates missing workload records
→ Scheduler assigns nodes
→ Runtime starts containers
→ Discovery publishes healthy endpoints
→ Gateway routes traffic
```

### Node scale-up

```text
Scheduler cannot place a workload
→ workload becomes Pending with an unschedulable reason
→ Node autoscaler detects unschedulable demand
→ selects a matching NodePool (requirements, labels, cost, region policy)
→ Infrastructure creates a machine (operation id recorded first)
→ cloud-init installs Runtime → node joins Forge network → registers
→ health checks pass
→ Scheduler places the pending workload
```

### Scale-down

```text
Demand decreases
→ workload replicas scale down (stabilization window respected)
→ a node becomes underutilized
→ Node autoscaler marks the node Draining
→ Scheduler stops placing new workloads there
→ remaining workloads move to other nodes, honouring disruption budgets
→ node becomes empty
→ Infrastructure deletes the machine
```

---

## 5. Safeguards

Non-optional, enforced by the autoscaler regardless of policy:

| Safeguard | Behaviour |
|---|---|
| Cooldown periods | no oscillation between adjacent decisions |
| Stabilization windows | decisions use the window's extreme, not the latest sample |
| Minimum availability | never below `minReplicas`, never below policy floor |
| Scale-rate limits | `maxReplicasPerMinute` up and down; per-pool node creation limits |
| Maximum cost limits | a pool refuses to grow past its budget ceiling |
| Node draining | machines are drained, never killed with workloads on them |
| Disruption budgets | evictions respect per-application budgets |
| Deployment freeze | no scale-down while a deployment is progressing |
| Stateful protection | database primaries and volume-attached workloads are never drained for bin-packing |
| Metric-outage fallback | missing metrics hold the last known good count; never scale to zero on absence |
| Manual override | an operator override always wins and expires explicitly |

Every decision writes its inputs, the formula result, the applied clamp, and the resulting
replica count to `ScalingPolicy.status` and to the audit log — so "why did this scale?" is
always answerable after the fact.

---

## 6. Scale to zero

Only opt-in workload classes scale to zero — models (`scaleToZeroAfter`), agents, and
cron-driven workers. HTTP applications default to `minReplicas >= 1` unless the policy
explicitly enables zero and a request-buffering route exists to absorb the cold start.

```yaml
scaling:
  minReplicas: 0
  maxReplicas: 4
  scaleToZeroAfter: 15m
```

```text
No active requests → model scales to zero
→ request arrives → run becomes Pending
→ Autoscaler requests GPU capacity if necessary
→ Runtime starts the model server
→ readiness check passes → request processed
```

---

## 7. Local gate and troubleshooting

Acceptance gate (Docker provider, sequential Compose):

```bash
make demo DEMO=24
```

That run covers HTTP scale-up/down, a 20,000-job worker backlog, node
scale-up from pending placements, safe drain/delete on scale-down, plus
manual override and metric-outage fallback visibility on
`ScalingPolicy.status`.

| Symptom | Check |
|---|---|
| Replicas never leave `minReplicas` | Gateway/Events admin metrics reachable (`GET …/admin/metrics?application=` / `?queue=`); policy `status.lastRecommendation` / `metricOutageMode` |
| Pending placements stuck | Eligible `NodePool` with room under `scaling.maxNodes`; Infrastructure Ready nodes joining Control |
| Node never drains | Workload desired count actually lowered on the Deployment; underutilization window elapsed; no pending demand |
| Override ignored | `PUT …/scalingpolicies/{name}/override` with current `resourceVersion`; status shows `manualOverride` until TTL |

Operator override/schedule details:
[docs/operations/autoscaler-overrides.md](../operations/autoscaler-overrides.md).
