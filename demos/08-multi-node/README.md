# Demo 08: Multi-node scheduler (epic gate)

End-to-end acceptance gate for epic 08 (multi-node scheduler). The script brings
up PostgreSQL, the local registry, Control, and **two** Runtime agents
(`node-a` / `node-b`, 4 slots each); deploys four replicas; then proves:

```text
Phase 1 — distribute
  deploy demo (replicas=4) with least-allocated + soft anti-affinity
  → GET /v1/placements shows node-a=2, node-b=2

Phase 2 — reschedule
  stop Runtime agent node-b (heartbeats stop)
  → GET /v1/nodes marks node-b offline
  → lost placements are rescheduled; node-a hosts all 4 placed replicas
```

Assertions use the Control node fleet and placement APIs (not log scraping).

This pre-09 demo sets `FORGE_AUTH_MODE=dev` explicitly (Control defaults to `enforce` as of `09.06`). Runtime
agents mount the host Docker socket — a privileged local-dev convenience, not
for production. Control owns workload lifecycle (`FORGE_LIFECYCLE_OWNER=control`).

## Run

From the repository root:

```bash
make demo DEMO=08
```

Expect a final `demo 08 PASSED` line and exit code `0`.

Optional phase split:

```bash
./demos/08-multi-node/run.sh --phase distribute
./demos/08-multi-node/run.sh --phase reschedule   # runs distribute then reschedule
```

## What this demo checks

* Two equal-capacity agents register and heartbeat as `node-a` / `node-b`.
* Four replicas distribute evenly (2+2) under `least-allocated` + soft anti-affinity.
* Stopping `forge-runtime-b` marks `node-b` offline; its replicas are rescheduled
  onto `node-a` (or briefly pending then drained if capacity-bound).
* Compose Control/Runtime agents are stopped on exit; managed demo containers
  are removed.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness + placement/node APIs |
| `FORGE_RUNTIME_A_URL` | `http://127.0.0.1:4102` | Runtime node-a readiness |
| `FORGE_RUNTIME_B_URL` | `http://127.0.0.1:4112` | Runtime node-b readiness |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `FORGE_NODE_HEARTBEAT_TIMEOUT_S` | `8` | Shortened offline detection |
| `FORGE_RESCHEDULE_GRACE_S` | `3` | Flap suppression before reschedule |
| `FORGE_SCHEDULER_STRATEGY` | `least-allocated` | Placement strategy |
| `FORGE_LIFECYCLE_OWNER` | `control` | Control creates/stops workloads |
| `FORGE_AUTH_MODE` | `dev` (explicit in `run.sh` / overlay) | Insecure bypass; Control defaults to `enforce` as of `09.06` |

`docker-compose.yml` in this directory is an overlay on the root `compose.yaml`
that adds `forge-runtime-b` and shortens scheduler timeouts.

No secrets are stored. CLI config lives under a temporary `XDG_CONFIG_HOME`
removed on exit.

## Demo image

Built from `demos/07-rolling-deployment/apps/demo/` as
`localhost:5000/demo:multi-node`. The app satisfies the epic-01 runtime contract
(`PORT`, `/`, `/health/live`, `/health/ready`).

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. On exit
the script deletes remaining demo deployments, removes managed demo containers,
and stops Runtime agents and Control. PostgreSQL and the registry may remain
for the foundation stack. Use `make stop` or `make reset` for a full teardown.
