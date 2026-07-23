# Seed agents

Packaged YAML definitions under `services/forge-agents/agents/` (plus the
`fixture-echo` CI helper). Listed via `GET /v1/agents` and `forge agent list`.

Destructive tools are always approval-gated: declaring `runtime.restart` does
not let an agent execute it without `forge agent approve` (or the matching API).

## deployment-investigator

Diagnose failing deployments: read desired/actual status, search recent logs,
query readiness-related metrics, and optionally request a restart.

| | |
|---|---|
| Model | `local-general` |
| Tools | `deployment.read`, `logs.search`, `metrics.query`, `runtime.restart` |
| Permissions | `project:read`, `deployment:read`, `logs:read`, `metrics:read`, `runtime:restart` |
| Limits | `max_steps: 10`, `timeout_seconds: 120` |
| Destructive | `runtime.restart` (approval required) |

```bash
forge agent run deployment-investigator --input "investigate dep-1" \
  --project proj-a --deployment dep-1 --dry-run
# Force the destructive path (pauses at awaiting_approval):
forge agent run deployment-investigator --input "restart dep-1" \
  --project proj-a --deployment dep-1 --tool runtime.restart --dry-run
forge agent deny <approval-id> --project proj-a --reason "not yet"
```

## log-summarizer

Search correlated logs and summarize them with the models generate tool.

| | |
|---|---|
| Model | `local-general` |
| Tools | `logs.search`, `models.generate` |
| Permissions | `logs:read`, `models:generate` |
| Limits | `max_steps: 8`, `timeout_seconds: 90` |
| Destructive | none |

```bash
forge agent run log-summarizer --input "errors x3" --project proj-a --dry-run --json
```

## docs-assistant

Read stored documentation objects and answer with `models.generate`.

| | |
|---|---|
| Model | `local-general` |
| Tools | `storage.get`, `models.generate` |
| Permissions | `storage:read`, `models:generate` |
| Limits | `max_steps: 8`, `timeout_seconds: 90` |
| Destructive | none |

## release-reviewer

Review a release candidate from deployment status and logs, then draft a
summary via models.

| | |
|---|---|
| Model | `local-general` |
| Tools | `deployment.read`, `logs.search`, `models.generate` |
| Permissions | `deployment:read`, `logs:read`, `models:generate` |
| Limits | `max_steps: 10`, `timeout_seconds: 120` |
| Destructive | none |

## infra-health

Check infrastructure health via metrics, deployment status, and recent logs.

| | |
|---|---|
| Model | `local-general` |
| Tools | `metrics.query`, `deployment.read`, `logs.search` |
| Permissions | `metrics:read`, `deployment:read`, `logs:read` |
| Limits | `max_steps: 8`, `timeout_seconds: 90` |
| Destructive | none |

## Least privilege

Each seed agent declares only the tools it needs and the exact permissions those
tools require. Only `deployment-investigator` declares a destructive tool; the
run engine still pauses in `awaiting_approval` before executing it.
