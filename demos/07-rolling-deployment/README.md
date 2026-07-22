# Demo 07: Rolling deployment (reconcile epic gate)

End-to-end acceptance gate for epic 07 (deployment reconciliation). The script
brings up PostgreSQL, the local registry, Control, Runtime, and Gateway; builds
three demo images (`v1`, `v2`, `v3-broken`); then proves:

```text
Scenario A — healthy rolling update
  deploy demo:v1 (replicas=2) → converge → gateway shows "v1"
  PATCH image → demo:v2 → rolling update → 0 failed probe requests → "v2"

Scenario B — unhealthy rollout + automatic rollback
  PATCH image → demo:v3-broken (/health/ready always 503)
  controller waits until FORGE_ROLLOUT_TIMEOUT_S
  rolls back to v2; gateway shows "v2"; no v3 containers remain
  GET /v1/deployments/{id}/history records both transitions
```

Traffic is served through Forge Gateway on `demo.localhost`
(`FORGE_HOST_PATTERN={service}.localhost`, service name `demo`).

Until Identity epic 09, Control / Runtime / Gateway use `FORGE_AUTH_MODE=dev`.
Runtime mounts the host Docker socket — a privileged local-dev convenience, not
for production. Control owns workload lifecycle (`FORGE_LIFECYCLE_OWNER=control`).

## Run

From the repository root:

```bash
make demo DEMO=07
```

Expect a final `demo 07 PASSED` line and exit code `0`.

Optional scenario split (both still need a live stack):

```bash
./demos/07-rolling-deployment/run.sh --scenario A
./demos/07-rolling-deployment/run.sh --scenario B   # runs A then B
```

## What this demo checks

* Rolling update v1 → v2 with replicas=2 and batch size 1 keeps at least one
  ready replica; a concurrent gateway probe sees zero non-2xx responses.
* An intentionally broken v3 (readiness always fails) is automatically rolled
  back to the last healthy image (v2) after the shortened rollout timeout.
* Deployment history includes `deployed` (v2) and `rolled_back` (restored v2).
* Compose Control/Runtime/Gateway are stopped on exit; managed demo containers
  are removed.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_GATEWAY_URL` | `http://127.0.0.1:4000` | Gateway health + data-plane curls |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness + PATCH/history |
| `FORGE_RUNTIME_URL` | `http://127.0.0.1:4102` | Runtime readiness |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `FORGE_HOST_PATTERN` | `{service}.localhost` | Hostname template (set by `run.sh`) |
| `FORGE_RECONCILE_INTERVAL_MS` | `1000` | Faster Control reconcile ticks |
| `FORGE_ROLLOUT_TIMEOUT_S` | `90` | Shortened vs platform default 120; enough for 2-replica rolls |
| `FORGE_ROLLOUT_BATCH_SIZE` | `1` | One-at-a-time rolling update |
| `FORGE_LIFECYCLE_OWNER` | `control` | Control creates/stops workloads |
| `FORGE_AUTH_MODE` | `dev` in Compose | Temporary auth bypass until `09.06` |

`docker-compose.yml` in this directory is an overlay on the root `compose.yaml`
that injects the shortened rollout timeout into Control.

No secrets are stored. CLI config lives under a temporary `XDG_CONFIG_HOME`
removed on exit.

## Demo images

Built from `apps/demo/` with Docker build args:

| Tag | `VERSION` | `READY_FAIL` |
|---|---|---|
| `localhost:5000/demo:v1` | `v1` | `false` |
| `localhost:5000/demo:v2` | `v2` | `false` |
| `localhost:5000/demo:v3-broken` | `v3` | `true` |

The app satisfies the epic-01 runtime contract (`PORT`, `/`, `/health/live`,
`/health/ready`).

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. On exit
the script deletes remaining demo deployments, removes managed demo containers,
and stops Gateway, Runtime, and Control. PostgreSQL and the registry may remain
for the foundation stack. Use `make stop` or `make reset` for a full teardown.
