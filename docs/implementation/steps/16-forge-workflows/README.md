# Steps for epic 16-forge-workflows

Epic: [`../../epics/16-forge-workflows.md`](../../epics/16-forge-workflows.md) · Status: **In progress**

Durable multi-step orchestration (Elixir/OTP, `services/forge-workflows`, host port `4302`, demo `demos/16-agent-workflow`).

| Step | Title | Status | Depends on |
|---|---|---|---|
| [16.01](16.01-skeleton-otp-health.md) | Skeleton OTP + health | Complete | 00, 01 |
| [16.02](16.02-definitions-durable-state.md) | Definitions + durable run state | Not started | 16.01 |
| [16.03](16.03-step-primitives.md) | Step primitives | Not started | 16.02 |
| [16.04](16.04-event-triggers-agent-steps.md) | Event triggers + agent steps | Not started | 16.03, 11, 15 |
| [16.05](16.05-human-approval-restarts.md) | Human approval across restarts | Not started | 16.04 |
| [16.06](16.06-compensation-rollback.md) | Compensation/rollback via Control | Complete | 16.05, 02/07 |
| [16.07](16.07-demo-and-gate.md) | Demo `16-agent-workflow` + gate | Not started | 16.06 |

Next to implement: **16.02**.
