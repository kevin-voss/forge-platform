# Capstone runbook — `demos/09-full-platform`

Operations guide for the north-star full-platform demo (epic 19).

## Folder naming

| Folder | Purpose |
|---|---|
| `demos/09-full-platform` | Capstone (this demo) |
| `demos/09-platform-identity` | Identity epic gate — **do not merge or rename** |

## One-command start / accept

```bash
# From repo root
make demo DEMO=09-full-platform          # → start.sh
make demo-accept DEMO=09-full-platform   # → accept.sh

# Or from the demo folder
cd demos/09-full-platform
./start.sh
./accept.sh
```

Convenience alias: `make demo-full` (same as `DEMO=09-full-platform`).

## Modes

| Mode | How | What runs |
|---|---|---|
| **CI subset** (default) | `CI_SUBSET=true` | postgres + Models + Agents + Memory + Workflows (fake backends); Step 19 test list via unit + AI + recovery loop |
| **Full stack** | `CI_SUBSET=false` | Entire platform + polyglot product via `deploy.sh` (kept running), then acceptance |

Determinism for CI:

```bash
export FORGE_MODELS_BACKEND=fake
export FORGE_AGENTS_TOOLS_MODE=fake
export FORGE_WORKFLOWS_AGENTS_MODE=fake
export FORGE_WORKFLOWS_CONTROL_MODE=fake
```

## North-star recovery loop

```text
deploy product → CAPSTONE_BREAK readiness fail → deployment.failed
  → incident-response workflow → agent diagnosis (telemetry + memory)
  → human approval → Control rollback → report + deployment.completed
  → product healthy again
```

Manual mid-run inspection (stack already up):

```bash
./scenario/break-release.sh accept
# while awaiting_approval:
curl -sS "$FORGE_WORKFLOWS_URL/v1/runs" -H "X-Forge-Project: capstone"
curl -X POST "$FORGE_WORKFLOWS_URL/v1/approvals/<id>/approve" \
  -H "X-Forge-Project: capstone" -H 'content-type: application/json' \
  -d '{"decided_by":"operator","reason":"rollback"}'
```

## Failure dumps

`accept.sh` prints per-test pass/fail. On any failure it dumps correlated logs from
Workflows / Agents / Models / Memory / Control / Gateway / Events and exits non-zero.
Teardown always runs unless `FORGE_ACCEPT_KEEP=1`.

## Security checklist

* Identity enforce for Control + Secrets; product PAT via introspect
* No hardcoded secrets; Secrets master key generated per run
* Logs masked (`FORGE_INJECT_MASK_IN_LOGS=true`)
* Rollback only after Workflows approval
* Audit: workflow run steps + approval decision + report artifact

## Scenario walkthrough

See [`scenario/walkthrough.md`](scenario/walkthrough.md).
