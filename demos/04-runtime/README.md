# Demo 04: Forge Runtime

End-to-end acceptance gate for Forge Runtime. The script brings up PostgreSQL,
the local registry, Control, and Runtime; builds and pushes the Step-01 Go demo
image; then drives a full CLI → Control → Runtime deploy path:

```text
Forge CLI → Forge Control → Forge Runtime → Docker Engine → demo-go container
                                   │
                          reports actual state ↑
```

## What this demo checks

* Hierarchy + deployment are created via the CLI (`forge project|env|app|service|deployment`).
* Runtime converges the desired deployment: pulls `localhost:5000/demo-go:latest`,
  starts a deterministically named/labeled container, probes health, and reports
  Control status `active`.
* The app answers on the published host port; Runtime log fetch returns lines.
* Deleting the Control deployment removes the managed container.
* A bad image converges to Control status `failed` (no dangling container).
* Creating the same deployment twice (idempotency key) yields one container.
* Container name is `forge-<deploymentId>` with labels `forge.deployment_id`,
  `forge.managed=true`, and `forge.node_id`.

Until Identity epic 09, Control and Runtime use `FORGE_AUTH_MODE=dev`. Runtime
mounts the host Docker socket — a privileged local-dev convenience, not for
production.

## Run

From the repository root:

```bash
make demo DEMO=04
```

Expect a final `Demo 04 passed.` line and exit code `0`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness + status polls |
| `FORGE_RUNTIME_URL` | `http://127.0.0.1:4102` | Runtime readiness, node state, logs |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `DEMO_IMAGE` | `localhost:5000/demo-go:latest` | Image deployed by the gate |
| `FORGE_RECONCILE_INTERVAL_SECONDS` | `3` (set by `run.sh`) | Faster desired→actual cycles for the demo |
| `FORGE_AUTH_MODE` | `dev` in Compose | Temporary auth bypass until `09.06` |

No secrets are stored. CLI config lives under a temporary `XDG_CONFIG_HOME`
removed on exit.

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. On exit
the script deletes remaining demo deployments, removes managed containers for
those ids, and stops Control + Runtime. PostgreSQL and the registry may remain
for the foundation stack. Use `make stop` or `make reset` for a full teardown.
