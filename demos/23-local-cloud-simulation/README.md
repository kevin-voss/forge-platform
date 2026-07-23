# Demo 23: Local cloud simulation (epic gate)

End-to-end acceptance gate for epic 23. Proves Forge Infrastructure creates and
deletes runtime nodes through the Docker provider while the scheduler treats
those nodes like any other fleet member — no special scheduler code.

```text
InfrastructureProvider(docker-local) + NodePool(replicas=2)
  → two forge-runtime containers start, bootstrap, join Control
  → deploy Application replicas onto provider-created nodes
  → scale NodePool to 3 → third node Ready + schedulable
  → scale NodePool to 2 → drain → delete; no orphan containers / stuck ops
```

Compose builds are sequential (`COMPOSE_PARALLEL_LIMIT=1`). The gate starts
Control + Infrastructure (not the seed `forge-runtime` agent); capacity comes
from the Docker provider.

## Run

From the repository root:

```bash
make demo DEMO=23
```

Expect a final `demo 23 PASSED` line and exit code `0`.

## Optional cloud targets (never CI)

Billable providers are opt-in and require explicit credentials. They are
**not** part of `make demo DEMO=23` or any CI job:

```bash
# Documented only — prints requirements and exits non-zero unless confirmed.
FORGE_DEMO_TARGET=hetzner FORGE_DEMO_CLOUD_CONFIRM=1 make demo DEMO=23
FORGE_DEMO_TARGET=aws     FORGE_DEMO_CLOUD_CONFIRM=1 make demo DEMO=23
FORGE_DEMO_TARGET=azure   FORGE_DEMO_CLOUD_CONFIRM=1 make demo DEMO=23
```

| Target | Required env | Notes |
|---|---|---|
| `hetzner` | `FORGE_INFRA_HETZNER_API_TOKEN` or Secrets ref | Creates real Hetzner servers |
| `aws` | `FORGE_INFRA_AWS_CREDENTIALS_JSON` | EC2/VPC primitives only |
| `azure` | `FORGE_INFRA_AZURE_CREDENTIALS_JSON` | VM/VNet primitives only |

Without `FORGE_DEMO_CLOUD_CONFIRM=1`, cloud targets refuse to run.

## What this demo checks

* `InfrastructureProvider` + `NodePool` apply via `forge apply`
* Provider-created nodes reach `Ready` (`/v1/forgenodes`) and register online on the scheduler fleet (`/v1/nodes`)
* Workloads place on those nodes without scheduler special-cases
* Scale-up adds a Ready node; scale-down drains and deletes with no leftover
  `forge.managed=true` containers and no stuck ledger operations
* Optional cloud paths are documented and gated out of CI

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API |
| `FORGE_INFRA_URL` | `http://127.0.0.1:4111` | Infrastructure health / ledger |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `COMPOSE_PARALLEL_LIMIT` | `1` | Sequential Compose builds |
| `FORGE_DEMO_TARGET` | `docker` | `docker` (default) or `hetzner`/`aws`/`azure` |

## Fixtures

`fixtures/nodepool-docker.yaml` — `InfrastructureProvider(docker-local)` +
`NodePool(local-docker-pool, replicas=2)`.

## Docs

* Epic: [`docs/implementation/epics/23-forge-infrastructure.md`](../../docs/implementation/epics/23-forge-infrastructure.md)
* Architecture: [`docs/architecture/provider-model.md`](../../docs/architecture/provider-model.md)
* Service: [`services/forge-infrastructure/README.md`](../../services/forge-infrastructure/README.md)
