# Steps for epic 25-scheduling-enhancements

Atomic steps for [Scheduling enhancements](../../epics/25-scheduling-enhancements.md).

| Step | N | File | Status |
|---|---:|---|---|
| 25.01 | 168 | [25.01-resource-requests-limits-and-capacity.md](25.01-resource-requests-limits-and-capacity.md) | Not started |
| 25.02 | 169 | [25.02-labels-selectors-taints-tolerations.md](25.02-labels-selectors-taints-tolerations.md) | Not started |
| 25.03 | 170 | [25.03-affinity-and-topology-spread.md](25.03-affinity-and-topology-spread.md) | Not started |
| 25.04 | 171 | [25.04-priority-preemption-and-disruption-budgets.md](25.04-priority-preemption-and-disruption-budgets.md) | Not started |
| 25.05 | 172 | [25.05-gpu-and-stateful-placement.md](25.05-gpu-and-stateful-placement.md) | Not started |
| 25.06 | 173 | [25.06-demo-25-ha-placement.md](25.06-demo-25-ha-placement.md) | Not started |

Implement with [`../../IMPLEMENT_STEP.md`](../../IMPLEMENT_STEP.md) (`STEP_ID=25.01` first).

All six steps extend the epic-08 scheduler seam (`Scheduler.place(PlacementRequest) → PlacementDecision`, the `placements`/`nodes` tables) additively — see [epic 25 § Relationship to shipped epics](../../epics/25-scheduling-enhancements.md#relationship-to-shipped-epics) for the compatibility rule every step must honor.
