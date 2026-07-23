# Steps for epic 15-forge-agents

Epic: [`../../epics/15-forge-agents.md`](../../epics/15-forge-agents.md) · Status: **In progress**

Permission-aware agent runtime (Python, `services/forge-agents`, host port `4301`, demo `demos/15-agent-runtime`).

| Step | Title | Status | Depends on |
|---|---|---|---|
| [15.01](15.01-skeleton.md) | Skeleton | Complete | 00, 01 |
| [15.02](15.02-agent-registry-yaml.md) | Agent registry + YAML definitions | Not started | 15.01 |
| [15.03](15.03-tool-registry-permissions.md) | Tool registry + per-call permission checks | Not started | 15.02 |
| [15.04](15.04-run-engine.md) | Run engine: max steps, timeouts, history | Not started | 15.03, 14 |
| [15.05](15.05-platform-tools.md) | Platform tools | Not started | 15.04, 02/04/12/13/14/11 |
| [15.06](15.06-human-approval.md) | Human approval for destructive tools | Not started | 15.05 |
| [15.07](15.07-seed-agents-cli.md) | Seed agents + CLI `forge agent` | Not started | 15.06, 03 |
| [15.08](15.08-demo-and-gate.md) | Demo `15-agent-runtime` + gate | Not started | 15.07 |

Next to implement: **15.02**.
