# Demo 15: Agent runtime

End-to-end acceptance gate for epic 15 (Forge Agents). A standalone Compose
stack brings up `forge-models` (`FORGE_MODELS_BACKEND=fake`) and
`forge-agents` (`FORGE_AGENTS_TOOLS_MODE=fake`) with failing-deployment
fixtures. The seed `deployment-investigator` agent inspects status/logs/metrics,
produces a diagnosis, and requests `runtime.restart` — which pauses in
`awaiting_approval` and is **not** executed without a human.

```text
1. bring up forge-agents + forge-models (fake) with failing-deployment fixtures
2. run deployment-investigator (deterministic dry_run plan)
3. agent reads deployment status, logs, readiness failure (registered tools only)
4. agent produces a diagnosis + recommends restart
5. restart is requested → run is awaiting_approval; nothing is restarted
6. a hallucinated tool call (shell.exec) is rejected
7. run history is complete and auditable; limits respected
```

```text
acceptance.sh (host)
        │  HTTP
        ▼
forge-agents :4301  ──fake tools──► fixtures (dep-failing not ready)
        │
        └── dry_run FakeModelClient plan
                deployment.read → logs.search → metrics.query → runtime.restart
                                                          └─ awaiting_approval

forge-models :4300 (fake; wired for the stack, not required for dry_run decisions)
```

## What this demo checks

* OpenAPI contracts for agents (+ models) parse and document the used paths.
* `deployment-investigator` is registered and least-privilege.
* Fake fixtures expose a failing deployment (`ready=false`, readiness probe errors, `up=0`).
* The investigator uses only registered, permitted tools to gather evidence.
* Diagnosis + restart recommendation are visible in the auditable run history.
* `runtime.restart` creates a pending approval; the tool is **not** executed.
* An unregistered tool (`shell.exec`) is rejected with `unknown_tool`.
* Declared `max_steps` / `timeout_seconds` bounds are respected.
* Deterministic CI path: no live Control/Runtime/Observe and no external models.

## Run

From the repository root:

```bash
make demo DEMO=15
```

Expect a final `demo 15 PASSED` line and exit code `0`. On failure the script
dumps agent/models logs plus recent runs, then tears down with
`docker compose down -v`.

Optional bring-up only (leaves the stack running):

```bash
./demos/15-agent-runtime/run.sh --phase=up
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_AGENTS_URL` | `http://127.0.0.1:4301` | Host agents API + readiness |
| `FORGE_MODELS_URL` | `http://127.0.0.1:4300` | Host models API + readiness |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | Deterministic platform tool fixtures |
| `FORGE_MODELS_BACKEND` | `fake` | Deterministic models backend |
| `FORGE_AGENTS_PROJECT` | `demo-15` | `X-Forge-Project` scope |
| `FORGE_AGENTS_DEPLOYMENT` | `dep-failing` | Fixture deployment id |
| `FORGE_LOG_LEVEL` | `info` | Service log level |

## Fixtures

| File | Mounted as | Meaning |
|---|---|---|
| `fixtures/deployment-status.json` | `deployment.read` | `ready=false`, degraded |
| `fixtures/logs.json` | `logs.search` | readiness probe failures |
| `fixtures/metrics.json` | `metrics.query` | `up=0` for the deployment |

## Security notes

* No credentials or secrets; tools and model are fully local/fake.
* The gate proves the no-unauthorized-restart guarantee: the demo never calls
  approve on the pending `runtime.restart`.
* Suitable for CI regression of the agents safety contract.
