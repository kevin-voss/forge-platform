# Scenario walkthrough — broken release → recovery

End-to-end narrative of the north-star loop (`specs.md` Step 19).

## 1. Deploy

Developer deploys the polyglot incident product through Build → Runtime → Gateway.
Identity enforces roles: **viewer** cannot deploy; **developer** can.
Secrets inject `APP_SHARED_SECRET` + `DATABASE_URL` (managed Postgres). Observe
records a distributed trace across api / admin / classify.

## 2. Break

A deliberately broken release sets `CAPSTONE_BREAK=true`. Product `/health/ready`
returns `503` with `{status:not_ready, error:capstone_break}`.

## 3. Detect

Readiness failure becomes `deployment.failed` on Forge Events (CI subset uses the
documented Workflows `/v1/triggers/test` with the same event shape).

## 4. Diagnose

`incident-response` starts:

1. Parallel diagnostics (logs / metrics / deployment.read)
2. `deployment-investigator` agent run with `memory.search`
3. Diagnosis cites telemetry + a similar historical incident from Memory
4. Workflow pauses at `approve-rollback` — **no rollback yet**

Mid-run: restarting `forge-workflows` resumes without repeating completed steps.

## 5. Approve → rollback

Human approves. Workflow calls Control rollback, writes the final report
(`scenario/expected-report.md` shape), emits `deployment.completed`.

Deny path: `do-rollback` skipped; run closes without changing the deployment.

## 6. Healthy again

Product returns to the last healthy image / readiness succeeds. All actions are
auditable on the workflow run (steps, approval, report ref).

## Reproduce

```bash
cd demos/09-full-platform
./start.sh          # CI subset by default
./accept.sh         # runs tests/ including scenario/break-release.sh
```
