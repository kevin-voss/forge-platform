# Epic 38: AI infrastructure scheduling

## Status

Planning

## Milestone

**M3 — Global platform.** First of the six M3 epics (38–43); 43 is the M3 exit capstone.

## Goal

Make GPU capacity and model serving first-class, schedulable, autoscalable platform resources instead of an always-on process behind epic 14's API. When this epic is done, GPU-bearing nodes are discovered and labeled, a `Model` resource can declare its GPU/memory needs and scale from zero to N replicas and back, request batching and token-throughput metrics are collected, and a request against a scaled-to-zero model triggers capacity acquisition, model-server startup, and readiness before the request completes. Proven by `demos/38-gpu-model-scaling`.

## Why this epic exists

Epic 14 shipped a stable model-serving *API contract* with a single local backend; epic 15's agents already call it. Neither epic addresses the operational reality of model serving: GPUs are scarce, expensive, and slow to warm, so models need their own placement and autoscaling story distinct from ordinary CPU workloads (epic 08/24). This epic gives the scheduler a GPU dimension and gives Models a declarative, scale-to-zero resource so GPU spend tracks actual demand.

## Relationship to shipped epics

Extends **epic 14 Forge Models** (adapter-based generate/embed/classify API) by promoting `Model` from an implicit always-on backend to a first-class declarative resource with placement and scaling; extends **epic 15 Forge Agents** by giving agent-invoked models real capacity management instead of an assumed-always-warm process; extends **epic 08 multi-node scheduler** and **epic 24 Forge Autoscaler** by adding GPU as a schedulable resource dimension alongside CPU/memory slots. Compatibility rule: epic 14's existing `POST /v1/models/{model}/generate|embed|classify` HTTP contract is unchanged — it is now served by placement-aware, possibly scaled-to-zero replicas instead of one static process; no client of epic 14's API needs to change.

## Primary code areas

* `services/forge-models/` — extends the registry and inference API with `Model` resource backing and warm-pool state
* `services/forge-control/scheduler/` (or its extracted successor) — GPU-aware placement extension to epic 08/25's strategies
* `services/forge-agents/tools/` — model-invocation tool updated for scale-from-zero latency handling
* `demos/38-gpu-model-scaling/` — scale-to-zero and scale-from-zero acceptance

## Suggested language

Python (Forge Models, per epic 14) plus the scheduler extension in whichever language epic 08/25 landed in (Kotlin, per epic 08's current module).

## Spec references

* `docs/architecture/standalone-cloud.md` § AI infrastructure scheduling
* `specs.md` → Step 14 (Forge Models), Step 15 (Forge Agents) — the serving and consumption baselines being extended
* `docs/implementation/epics/08-multi-node-scheduler.md` — placement strategy baseline gaining a GPU dimension

## Dependencies

* Epic [`14-forge-models`](14-forge-models.md) — model-serving API and registry being extended
* Epic [`15-forge-agents`](15-forge-agents.md) — the primary consumer of scaled model capacity
* Epic [`08-multi-node-scheduler`](08-multi-node-scheduler.md) — placement strategies gaining a GPU resource dimension
* Epic `24-forge-autoscaler` (catalogued, not yet materialized) — scale-to-zero/scale-from-zero replica mechanics
* Epic `25-scheduling-enhancements` (catalogued, not yet materialized) — GPU-class node affinity

## Out of scope for this epic

* Training or fine-tuning models (still out of scope, as in epic 14)
* Multi-region model placement (epic 39 handles region topology; this epic is single-region GPU placement)
* Cost-based provider selection for GPU capacity (epic 41)
* Plugin-loaded model runtimes beyond the built-in adapters (epic 43 adds the extension point)

## Portability contract

A product manifest never names a GPU instance type or cloud SKU; `runtime.gpu` is a portable capability class (`any`, `none`, or a named class like `a10-class`) resolved against whatever `NodePool`/`InfrastructureProvider` the operator installed. On local Docker (the CI default) GPU scheduling runs in a documented simulated-GPU fake mode — no real GPU required to pass the gate — while bare metal, Hetzner, AWS, and Azure resolve the same `Model` resource against real GPU-labeled nodes; the API and resource shape never differ by target.

```yaml
apiVersion: forge.dev/v1
kind: Model
metadata:
  name: support-classifier
  project: invoice-platform
  environment: production
spec:
  source: { type: registry, image: registry.forge.internal/forge-labs/models/classifier:1.2.0 }
  runtime: { gpu: any, memory: 8Gi }
  scaling: { minReplicas: 0, maxReplicas: 4, scaleToZeroAfter: 5m }
status:
  phase: Ready
  loadedReplicas: 0
  observedGeneration: 3
```

## Success demo

```bash
make demo DEMO=38
```

```text
No requests for scaleToZeroAfter → Model scales to zero replicas
Request arrives → Model run enters Pending
→ Autoscaler requests GPU capacity if none is free
→ Runtime starts the model server on a GPU-labeled node
→ readiness check passes
→ request is processed and the response returned
Sustained request load → Model scales beyond 1 replica; token-throughput
metrics visible per replica
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 38.01 | GPU node discovery + capability labels | Nodes report GPU presence/class to the scheduler |
| 38.02 | GPU-aware scheduling + `Model` resource | Placement strategy gains a GPU dimension; `Model` CRUD |
| 38.03 | Model loading state + warm pools | Track loading/ready/unloading; keep N warm replicas |
| 38.04 | Scale-to-zero and scale-from-zero flow | Idle timeout; Pending-run-triggers-capacity path |
| 38.05 | Request batching + token-throughput metrics | Batch concurrent requests; emit per-replica throughput |
| 38.06 | Model quotas + fallback routing + local-only mode | Per-project GPU quota; fallback when capacity unavailable |
| 38.07 | Demo `38-gpu-model-scaling` + epic gate | Scale-to-zero/from-zero acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* CI runs entirely on the simulated-GPU fake mode from `38.01`; real GPU hardware is exercised only on an optional, non-gating local/cloud run.
* Model warm-pool sizing is a per-`Model` `scaling` field, not a global platform setting.
* Scale-from-zero latency is bounded by a documented "cold start" budget in the demo; requests during cold start block (with a timeout) rather than failing immediately.
* External-provider model adapters (calling a hosted inference API) are optional and never required for the gate demo, matching the "no provider-managed service required" rule.

## Open questions

* Does request batching happen inside `forge-models` or at the scheduler/Gateway layer? Assumption: inside `forge-models`, close to the adapter, since batching semantics are model-specific.
* Is GPU capacity request-driven (as in the flow above) or pre-provisioned via NodePool minimums? Assumption: both — a `NodePool` can pre-provision a GPU floor, and the scale-from-zero flow additionally requests capacity on demand when the floor is exhausted.
* How is "simulated GPU" represented in the resource model so it's clearly not a real capability? Assumption: a `runtime.gpu: simulated` class recognized only in non-production install profiles, refused by policy (epic 33) outside of CI/dev.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **38.01 — GPU node discovery + capability labels** first: GPU-aware placement and the `Model` resource both depend on nodes being able to report GPU capability.
