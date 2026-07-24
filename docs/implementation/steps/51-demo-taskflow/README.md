# Steps for epic 51-demo-taskflow

Atomic steps for [Demo 1 — TaskFlow](../../epics/51-demo-taskflow.md). Product design:
[`../../../demo-projects/projects/01-taskflow.md`](../../../demo-projects/projects/01-taskflow.md).

> **Verification track.** Global `N` queue `181`–`186`, continuing after the platform queue (`N ≤ 173`). Requires epic **50** complete.

| N | Step | File | Status |
|---:|---|---|---|
| **181** | `51.01` | [51.01-scaffold-and-deploy.md](51.01-scaffold-and-deploy.md) | Complete |
| **182** | `51.02` | [51.02-managed-postgres-and-schema.md](51.02-managed-postgres-and-schema.md) | Complete |
| **183** | `51.03` | [51.03-identity-auth-and-roles.md](51.03-identity-auth-and-roles.md) | Not started |
| **184** | `51.04` | [51.04-secrets-injection.md](51.04-secrets-injection.md) | Not started |
| **185** | `51.05` | [51.05-e2e-browser-spec.md](51.05-e2e-browser-spec.md) | Not started |
| **186** | `51.06` | [51.06-demo-and-gate.md](51.06-demo-and-gate.md) | Not started |

Implement with [`../../IMPLEMENT_STEP.md`](../../IMPLEMENT_STEP.md) (`N = 181` first).
