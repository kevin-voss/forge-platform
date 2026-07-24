# Steps for epic 54-demo-orderpipe

Atomic steps for [Demo 4 — OrderPipe](../../epics/54-demo-orderpipe.md). Product design:
[`../../../demo-projects/projects/04-orderpipe.md`](../../../demo-projects/projects/04-orderpipe.md).

> **Verification track.** Global `N` queue `199`–`205`, continuing after the platform queue (`N ≤ 173`). Requires epic **50** complete.

| N | Step | File | Status |
|---:|---|---|---|
| **199** | `54.01` | [54.01-multi-service-scaffold.md](54.01-multi-service-scaffold.md) | Complete |
| **200** | `54.02` | [54.02-service-discovery-wiring.md](54.02-service-discovery-wiring.md) | Complete |
| **201** | `54.03` | [54.03-network-policy.md](54.03-network-policy.md) | Complete |
| **202** | `54.04` | [54.04-event-choreography.md](54.04-event-choreography.md) | Complete |
| **203** | `54.05` | [54.05-workflow-saga.md](54.05-workflow-saga.md) | Not started |
| **204** | `54.06` | [54.06-e2e-browser-spec.md](54.06-e2e-browser-spec.md) | Not started |
| **205** | `54.07` | [54.07-demo-and-gate.md](54.07-demo-and-gate.md) | Not started |

Implement with [`../../IMPLEMENT_STEP.md`](../../IMPLEMENT_STEP.md) (`N = 199` first).
