# forge-workflows

Elixir/OTP durable workflow orchestration service (epic 16). Host port **4302**.

Skeleton (16.01): supervision tree, health/identity HTTP surface, structured JSON
logs with request IDs, Compose wiring. Definitions, step primitives, triggers,
approvals, and compensation land in later steps. Epic gate:
`make demo DEMO=16` (`demos/16-agent-workflow`).

## Local

```bash
# from repo root
make service-run SERVICE=forge-workflows
make service-test SERVICE=forge-workflows

# or inside this directory
make compile
make run
make test
```

### Smoke

```bash
curl -fsS localhost:4302/health/live
curl -fsS localhost:4302/health/ready
curl -fsS localhost:4302/ | grep -q '"service":"forge-workflows"'
```

OpenAPI (canonical): [`contracts/openapi/forge-workflows.openapi.yaml`](../../contracts/openapi/forge-workflows.openapi.yaml).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (container) | Required; Compose maps host `4302` → `8080` |
| `FORGE_SERVICE_NAME` | `forge-workflows` | Identity `service` field |
| `FORGE_SERVICE_VERSION` | `0.1.0` | Identity `version` field |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | Logged at startup |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Bandit / OTP shutdown timeout |

## Architecture (16.01)

```text
ForgeWorkflows.Supervisor
├── ForgeWorkflows.RunRegistry      # placeholder for run processes (16.02+)
├── ForgeWorkflows.RunSupervisor    # DynamicSupervisor placeholder (16.02+)
└── Bandit → ForgeWorkflowsWeb.Router
```

No workflow engine, definitions, or Ecto repo yet.
