# Epic 24: Forge Autoscaler

## Status

In progress

## Milestone

**M1 — Standalone cloud core** (epics 20–25). M1 promises "nodes and workloads autoscale" on every install target; this epic is that promise.

## Goal

Stand up Forge Autoscaler — a Go service on port `4112` — that keeps replica counts and node counts right-sized without an operator watching a dashboard. When this epic is done, a `ScalingPolicy` resource drives workload replicas from CPU/memory, request-rate/latency/error-rate, and queue-depth signals; a cron/manual override layer handles predictable and emergency scaling; and a node-autoscaling loop grows and shrinks the cluster itself by driving `NodePool` counts through Forge Infrastructure (epic 23) when workloads go `Pending` for lack of capacity, then drains and removes nodes once they're idle. Every decision is a resource status update plus an audited event — nothing is a silent side effect. Proven by `demos/24-autoscaling`: synthetic load pushes replicas up, then past cluster capacity so a new Docker-provider node is created and a pending replica runs on it, then load stops and both replicas and the extra node scale back down.

## Why this epic exists

Epic 07 gave Forge a reconcile loop that holds `desiredReplicas` steady; epic 08 gave it a scheduler that places replicas across a fixed node fleet. Neither decides *how many* replicas or nodes should exist — that number has been a human typing `desired_replicas` into a request body. A standalone cloud platform that claims workloads and nodes "autoscale" (M1's promise) needs a component whose entire job is computing that number from real signals — utilization, request rate, queue backlog, schedules, and cluster pressure — and writing it where the existing controllers already know how to read it. This epic is that component: a pure decision-maker that never touches a container or a cloud API itself.

## Relationship to shipped epics

This epic introduces no breaking change and no rewrite. It adds one new resource kind workloads point at, one new resource kind nodes pools point at, and a small number of new *readers* of state that epics 05/08/11/12 already expose. The table below is the compatibility contract:

| Shipped/sibling epic | What it already owns | How epic 24 extends it | Compatibility rule |
|---|---|---|---|
| [`07-deployment-reconciliation`](07-deployment-reconciliation.md) | `Deployment.spec.desiredReplicas` (`02.05`) + the reconcile loop that converges actual replicas to it | The autoscaler becomes a second, automated caller of the existing desired-replicas write path — the same field/endpoint a human `forge scale` command already uses | **Additive field usage** — zero API or schema change to Deployment |
| [`08-multi-node-scheduler`](08-multi-node-scheduler.md) | Node fleet (`GET /v1/nodes`, `08.02`), pending-placement queue (`08.04`) | Node autoscaling reads fleet capacity and pending/unschedulable placements read-only; the scheduler additively learns to skip nodes whose `status.phase = Draining` | **Additive read** + one new, backward-compatible node phase value |
| [`05-forge-gateway`](05-forge-gateway.md) | Per-route RPS/latency/error-rate metrics | New `Gateway` metric-source adapter queries these read-only | Additive consumer, no Gateway change |
| [`11-forge-events`](11-forge-events.md) | Stream/consumer metrics (`forge_events_ready`, `forge_consumer_pending`, redelivery counters) | New `Queue` metric-source adapter queries these read-only; the same interface later points at Forge Queue (epic 28) with no autoscaler-side change | Additive consumer; the `MetricSource` interface itself is the facade |
| [`12-forge-observe`](12-forge-observe.md) | Prometheus/Tempo/Loki behind a thin correlation API | New `Observe` metric-source adapter runs PromQL for CPU/memory utilization read-only | Additive consumer, no Observe change |
| `20-declarative-resource-api` | Generic resource envelope, optimistic concurrency, watch API (`docs/concepts/resource-model.md`) | `ScalingPolicy` (environment-scoped) and `NodePool` status (cluster-scoped) are new kinds/fields registered in that envelope | **New resource kind** — envelope, API shape, and conventions unchanged |
| `23-forge-infrastructure` | `NodePool.spec`, the `Node` kind, `InfrastructureProvider` adapters, actual node create/delete | The autoscaler writes only `NodePool.status` — a kind `docs/concepts/resource-model.md` §5 already assigns to the "Node autoscaler" controller — never `Node` itself | **New resource kind + status ownership already assigned by the resource model** — no ownership conflict |

Plainly stated, because it is the design constraint the rest of the epic hangs off: **the autoscaler only ever changes numbers.** Its two mutating actions, end to end, are writing `Deployment.spec.desiredReplicas` (a workload replica count) and writing `NodePool.status.desiredNodes` (a node count). It never calls Runtime, never starts or stops a container, and never calls a cloud provider API — the epic-07 reconciler, the epic-08 scheduler, and the epic-23 Infrastructure controller are the only processes that do those things. Every one of the autoscaler's decisions is recorded as a status update on the resource it owns (`ScalingPolicy` or `NodePool`) plus a durable platform event (`resource.scalingpolicy.decided`, `resource.nodepool.decided`), so any scaling action is inspectable after the fact without log spelunking.

## Primary code areas

* `services/forge-autoscaler/` — new Go service: `ScalingPolicy`/`NodePool` controllers, metric-source adapters, evaluation loop, decision audit
* `services/forge-control/` — no code change; the autoscaler is a client of its existing deployment desired-state API
* `services/forge-gateway/`, `services/forge-events/`, `services/forge-observe/` — read-only metric consumers added; no changes to those services
* `demos/24-autoscaling/` — HTTP load generator, capacity-exhaustion scenario, queue-depth worker scenario, scale-down scenario
* `contracts/openapi/` — `ScalingPolicy` and `NodePool` status API surface

## Suggested language

Go. Matches the other internal platform services on the `41xx` port range (Gateway, Events, Observe) and keeps the autoscaler a small, restart-safe binary with no shared-process coupling to Control's Kotlin reconciler/scheduler modules.

## Spec references

* `docs/architecture/standalone-cloud.md` § Autoscaling — normative source for `ScalingPolicy`, `NodePool` scaling fields, and the metric-source model (M1)
* `docs/concepts/resource-model.md` § 5 Controller ownership — assigns `ScalingPolicy → Autoscaler` and `NodePool → Node autoscaler`, both implemented by this epic's single `forge-autoscaler` service
* `specs.md` has no dedicated autoscaling step — it stops at Step 19. This epic extends `specs.md` → Step 07 (reconciler desired/actual replica convergence) and Step 08 (scheduler capacity + pending-queue signals) rather than materializing a shipped spec step of its own
* `docs/implementation/MASTER_PLAN.md` → port `4112` reservation

## Dependencies

* [`20-declarative-resource-api`](20-declarative-resource-api.md) — resource envelope, optimistic concurrency, watch API that `ScalingPolicy`/`NodePool` are registered into (hard dependency)
* [`07-deployment-reconciliation`](07-deployment-reconciliation.md) — the only actuation path for workload replicas (hard dependency)
* [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — node fleet + pending-placement signal (hard dependency for node autoscaling, `24.06`–`24.07`)
* [`23-forge-infrastructure`](23-forge-infrastructure.md) — `NodePool.spec`, `Node` kind, provider adapters that actually create/delete nodes (hard dependency for `24.06`–`24.07`)
* [`05-forge-gateway`](05-forge-gateway.md) — RPS/latency/error-rate metric source (`24.03`)
* [`11-forge-events`](11-forge-events.md) — queue-depth/consumer-lag metric source (`24.04`)
* [`12-forge-observe`](12-forge-observe.md) — CPU/memory utilization metric source (`24.02`)
* `28-forge-queue` — future durable-queue metric source behind the same `MetricSource` interface (M2, not required to ship `24.04`)
* [`25-scheduling-enhancements`](25-scheduling-enhancements.md) — shares the pending/unschedulable signal; no ordering dependency either direction

## Out of scope for this epic

* Starting, stopping, or resizing containers directly (epic 07's job) or calling a cloud/provider API directly (epic 23's job)
* Vertical autoscaling (resizing one replica's CPU/memory request) — horizontal replica/node counts only
* Predictive or ML-based demand forecasting — reactive metrics plus declared schedules only
* True scale-to-zero for workers or workloads (floor is a configurable low, never 0, per the demo's worker scenario)
* Bin-packing / cost optimization beyond what epic 08's scheduler and epic 23's Infrastructure already do
* Multi-region scaling coordination (epic 39) and GPU-topology-aware scheduling nuance (epic 38 refines node selection further)
* Feature parity with provider-managed autoscalers (ASG, VMSS, managed Kubernetes cluster-autoscaler) — never required on any target

## Portability contract

The product manifest never gains new fields because of this epic — `spec.scaling.minReplicas` / `maxReplicas` / `policies` (already shown in the normative manifest) is still the entire customer-facing surface. `ScalingPolicy` and `NodePool` are platform-operator resources; a `ScalingPolicy` for a manifest's `scaling` block is either derived by an epic-20 facade or authored directly by the operator, but it is never something a product manifest embeds inline, and it never contains a provider name, machine type, region id, IP address, or credential.

| Target | What "create a node" means here | What never happens |
|---|---|---|
| Docker (default, CI gate) | Infrastructure's docker adapter starts another `forge-runtime` container on the demo network with a fresh node identity | No cloud API call, ever required |
| Bare metal | Infrastructure claims and boots an already-racked, pre-enrolled idle host | No new hardware is provisioned by this epic |
| Hetzner / AWS / Azure (opt-in, `FORGE_DEMO_TARGET`) | Infrastructure's provider adapter creates/destroys a VM via primitive-only APIs (Hetzner Cloud API, EC2, Azure VM) | `ScalingPolicy`/`NodePool` shapes and the autoscaler's decision loop are byte-identical to Docker; only the adapter differs, and these targets are never part of the default gate |

No provider-managed autoscaling group (ASG/VMSS) or managed-Kubernetes cluster-autoscaler is ever required on any target — Forge's own Node autoscaler is authoritative everywhere, so the same `ScalingPolicy`/`NodePool` resources produce identical behavior on a laptop and on a cloud VM fleet.

## Success demo

```bash
make demo DEMO=24
```

```text
Phase 1 — workload scale-up
  generate HTTP load against the demo service
  → CPU/RPS exceed ScalingPolicy targets → replicas climb from 2 toward maxReplicas

Phase 2 — node scale-up
  load pushes total demand past the cluster's current node capacity
  → a replica goes Pending (scheduler: no node with free capacity)
  → autoscaler detects unschedulable demand, selects NodePool "docker-pool"
  → Infrastructure (epic 23) starts a new forge-runtime container as a node
  → Runtime registers the node → scheduler places the pending replica

Phase 3 — worker scale-up (queue variant)
  publish 20,000 jobs to a Forge Events stream
  → queue depth breaches the worker ScalingPolicy target
  → worker replicas scale out (never to zero) and drain the backlog

Phase 4 — scale-down
  load stops → replicas converge back to minReplicas
  → the extra node sits idle past its cooldown
  → autoscaler marks it a drain candidate → scheduler stops placing on it
  → workloads already there move off → node goes empty
  → Infrastructure deletes the node
```

## Planned steps

| Step | N | Title | Status | Notes |
|---|---:|---|---|---|
| [24.01](../steps/24-forge-autoscaler/24.01-skeleton-scalingpolicy-and-metric-sources.md) | 160 | Service skeleton, `ScalingPolicy` resource, metric sources | Complete | Go service on `4112`; pluggable `MetricSource`; recommendations only, no actuation |
| [24.02](../steps/24-forge-autoscaler/24.02-workload-cpu-memory-autoscaling.md) | 161 | CPU/memory workload autoscaling | Not started | Utilization math, desired-replica formula, dampening; first real actuation |
| [24.03](../steps/24-forge-autoscaler/24.03-request-rate-and-latency-autoscaling.md) | 162 | Request-rate, latency, error-rate autoscaling | Not started | Gateway-sourced metrics + custom app metric hook |
| [24.04](../steps/24-forge-autoscaler/24.04-worker-queue-depth-autoscaling.md) | 163 | Worker autoscaling from queue signals | Not started | Events-sourced today, Queue-sourced later; scale-to-low, drain-safe shutdown |
| [24.05](../steps/24-forge-autoscaler/24.05-scheduled-scaling-and-overrides.md) | 164 | Scheduled scaling, manual override, safety fallbacks | Not started | Cron + timezone, override precedence, deployment freeze, metric-outage hold |
| [24.06](../steps/24-forge-autoscaler/24.06-node-autoscaling-scale-up.md) | 165 | Node autoscaling — scale up | Not started | Pending demand → NodePool selection → Infrastructure creates node |
| [24.07](../steps/24-forge-autoscaler/24.07-scale-down-draining-and-safeguards.md) | 166 | Scale down, draining, safeguards | Not started | Drain candidate scoring, disruption budgets, stateful-primary exclusion |
| [24.08](../steps/24-forge-autoscaler/24.08-demo-24-autoscaling.md) | 167 | Demo `24-autoscaling` + epic gate | Not started | All four phases; 20,000-job worker variant; epic acceptance gate |

## Assumptions

* The autoscaler resolves a `ScalingPolicy.spec.targetRef` (`{kind: Application, name}`) to the Application's current backing Deployment and writes `desiredReplicas` there via the exact endpoint/field epic 07 already ships (`02.05`/`07.01`) — no new write path, no new field on `Deployment`. Informally this is "writing desired replicas on the Application," since Application is the name the customer and the manifest use for the same workload.
* `NodePool.status` (`desiredNodes`, `currentNodes`, drain candidates, conditions) is owned exclusively by this epic's "Node autoscaler" controller, matching `docs/concepts/resource-model.md` §5 verbatim. `Node.status` remains owned by epic 23's Node controller, which only ever *reads* `NodePool.status` — no cross-kind status write, no exception to the one-controller-per-kind rule.
* Node "creation" is always mediated by epic 23's `InfrastructureProvider` adapter. The Docker adapter (default, CI) starts another `forge-runtime` container; this epic never shells out to Docker or a cloud SDK itself.
* Metric sources are pluggable behind one `MetricSource` interface (`Observe`, `Gateway`, `Queue`, `Runtime`, plus a deterministic `Fake` source for tests); each adapter queries metrics those services already export — no new telemetry backend.
* Evaluation is a bounded-interval poll loop (default 15s) reading the epic-20 watch API, not a hard real-time control loop; this is adequate for the demo's timescales and documented as a future tightening if needed.
* "Scale-to-low, not zero" for worker autoscaling means a configurable floor greater than zero (default 1) even at zero queue depth, to avoid cold-start latency on the next burst; true scale-to-zero is explicitly out of scope.
* Auth is `FORGE_AUTH_MODE=dev` until epic 09/hardening catches up; the autoscaler's cross-service calls are documented as needing scoped service-account tokens once that lands.

## Open questions

* Does the autoscaler write directly to `Deployment.spec.desiredReplicas`, or through a new Application-level facade? **Assumption:** directly to the existing field/endpoint (zero new API surface); if epic 20 later re-points Application at a different backing kind, only the autoscaler's target-resolution step needs to change, not its write contract.
* Where does "unschedulable demand" surface for node scale-up — a new endpoint, or the existing pending-placement query? **Assumption:** reuse and relax `GET /v1/placements?status=pending` (`08.04`) to allow a cluster-wide query with no `deployment` filter; no new resource kind for pending demand.
* Is `ScalingPolicy` one resource per target, or one per metric? **Assumption:** one `ScalingPolicy` per target (Application or Worker) holding a list of metrics, mirroring the product manifest's single `scaling` block.
* How does the Node autoscaler choose among several eligible `NodePool`s for the same pending demand? **Assumption:** `NodePool.spec` carries an operator-set `priority`; the autoscaler picks the lowest-priority-number pool whose `machineSelector`/labels match the pending workload's requirements and whose `maxNodes`/`maxCostPerHour` aren't already exhausted.
* How does a customer scale on an application-specific metric (e.g. queue length inside their own process)? **Assumption:** `metrics[].type: custom` with a PromQL-style `query` field the `ScalingPolicy` owner supplies against Observe; no new SDK is introduced by this epic, the application just needs to already export the metric.

## Next step to implement

**[24.02](../steps/24-forge-autoscaler/24.02-workload-cpu-memory-autoscaling.md) — CPU/memory workload autoscaling** (utilization math, desired-replica formula, dampening; first real actuation).
