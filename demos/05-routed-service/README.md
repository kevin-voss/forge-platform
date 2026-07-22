# Demo 05: Routed service (Forge Gateway)

End-to-end acceptance gate for Forge Gateway. The script brings up PostgreSQL,
the local registry, Control, Runtime, and Gateway; builds and pushes the
Step-01 Go, Rust, and Python demo images; deploys all three via the CLI; then
proves stable-hostname routing through the gateway on port `4000`:

```text
go.demo.localhost      ┐
rust.demo.localhost    ├─→ Forge Gateway :4000 ─→ host.docker.internal:<ephemeral port>
python.demo.localhost  ┘
```

## What this demo checks

* Go, Rust, and Python workloads are reachable by hostname through the gateway
  without callers knowing runtime-published ports.
* `X-Request-Id` is echoed to the client and forwarded to the upstream (verified
  via a short-lived echo upstream on the admin route table).
* Stopping a workload removes it from rotation (`503 no_healthy_upstream`)
  without restarting the gateway.
* A route change (admin replace) and a Control sync refresh both take effect
  without restarting the gateway.

Until Identity epic 09, Control / Runtime / Gateway use `FORGE_AUTH_MODE=dev`.
Runtime mounts the host Docker socket — a privileged local-dev convenience, not
for production. `/admin/routes` is unauthenticated in this mode.

## Hostnames

CI and this script use `curl -H 'Host: <name>' http://127.0.0.1:4000/` so no DNS
setup is required.

For interactive browsing, either rely on `*.localhost` → loopback (works on most
modern OSes) or add entries to `/etc/hosts`:

```text
127.0.0.1 go.demo.localhost rust.demo.localhost python.demo.localhost
```

Routes are derived with `FORGE_HOST_PATTERN={service}.demo.localhost` (services
named `go`, `rust`, and `python`).

## Run

From the repository root:

```bash
make demo DEMO=05
```

Expect a final `Demo 05 passed.` line and exit code `0`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_GATEWAY_URL` | `http://127.0.0.1:4000` | Gateway health, admin routes, data-plane curls |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness |
| `FORGE_RUNTIME_URL` | `http://127.0.0.1:4102` | Runtime readiness / node state |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `FORGE_HOST_PATTERN` | `{service}.demo.localhost` | Hostname template (set by `run.sh`) |
| `FORGE_ROUTE_SYNC_INTERVAL_SECONDS` | `3` (set by `run.sh`) | Faster sync for the gate |
| `FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS` | `2` (set by `run.sh`) | Faster unready detection |
| `FORGE_UPSTREAM_FAILURE_THRESHOLD` | `1` (set by `run.sh`) | Drop stopped upstreams quickly |
| `FORGE_RECONCILE_INTERVAL_SECONDS` | `3` (set by `run.sh`) | Faster Control→Runtime converge |
| `FORGE_AUTH_MODE` | `dev` in Compose | Temporary auth bypass until `09.06` |

No secrets are stored. CLI config lives under a temporary `XDG_CONFIG_HOME`
removed on exit.

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. On exit
the script deletes remaining demo deployments, removes managed containers for
those ids, and stops Gateway, Runtime, and Control. PostgreSQL and the registry
may remain for the foundation stack. Use `make stop` or `make reset` for a full
teardown.
