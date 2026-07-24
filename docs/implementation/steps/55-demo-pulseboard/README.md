# Steps for epic 55-demo-pulseboard

Atomic steps for [Demo 5 — PulseBoard](../../epics/55-demo-pulseboard.md). Product design:
[`../../../demo-projects/projects/05-pulseboard.md`](../../../demo-projects/projects/05-pulseboard.md).

> **Verification track.** Global `N` queue `206`–`211`, continuing after the platform queue (`N ≤ 173`). Requires epic **50** complete.

| N | Step | File | Status |
|---:|---|---|---|
| **206** | `55.01` | [55.01-scaffold-and-deploy.md](55.01-scaffold-and-deploy.md) | Complete |
| **207** | `55.02` | [55.02-http-request-rate-autoscaling.md](55.02-http-request-rate-autoscaling.md) | Complete |
| **208** | `55.03` | [55.03-node-autoscaling.md](55.03-node-autoscaling.md) | Complete |
| **209** | `55.04` | [55.04-observe-surfacing.md](55.04-observe-surfacing.md) | Complete |
| **210** | `55.05` | [55.05-e2e-browser-spec.md](55.05-e2e-browser-spec.md) | Not started |
| **211** | `55.06` | [55.06-demo-and-gate.md](55.06-demo-and-gate.md) | Not started |

Implement with [`../../IMPLEMENT_STEP.md`](../../IMPLEMENT_STEP.md) (`N = 206` first).
