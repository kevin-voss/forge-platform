# Demo 03: Forge CLI Control

An end-to-end acceptance gate for the Forge CLI. It builds `forge`, starts
PostgreSQL and Control, configures a local CLI profile, then recreates the
complete desired-state hierarchy using only CLI commands:

```text
project
├── environment (development)
└── application (web)
    └── service (api :8080)
        └── desired deployment (localhost:5000/demo-go:latest)
```

## What this demo checks

* The CLI builds from `tools/forge-cli` and talks to Control over HTTP only.
* A named profile (`demo` by default) stores the Control endpoint under an
  isolated `XDG_CONFIG_HOME` for the run.
* `project` / `env` / `app` / `service` / `deployment` create commands succeed
  with stable `--output json`, and every returned resource identifier is a UUID.
* List and status commands read the hierarchy back and match the created IDs.
* Table output still works for human inspection of deployment status.
* An unknown project id exits with code `3` and prints a useful error (including
  a request id) on stderr.
* The script never opens PostgreSQL or otherwise bypasses the Control API.

Control still records desired state only. The demo does not pull or run the
illustrative deployment image; workload execution is introduced in epic 04.
Until Identity epic 09, Control runs with the temporary `FORGE_AUTH_MODE=dev`
bypass.

## Run

From the repository root:

```bash
make demo DEMO=03
```

The demo rebuilds the CLI, rebuilds/recreates `forge-control`, starts
`forge-postgres` if needed, and stops Control through its exit trap. PostgreSQL
remains available for the foundation stack. Resources use unique names so
repeated runs do not collide.

To inspect the stack afterwards:

```bash
docker compose logs forge-control
curl -sf http://127.0.0.1:4001/health/ready
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness probe URL |
| `FORGE_ENDPOINT` | same as `FORGE_CONTROL_URL` | Endpoint written into the CLI profile |
| `FORGE_PROFILE` | `demo` | Named CLI profile used for the run |
| `CI` | `1` (set by `run.sh`) | Forces non-interactive CLI behavior |
| `FORGE_AUTH_MODE` | `dev` in Compose | Temporary development auth bypass until Identity step `09.06` |

No secrets are stored. The demo config file lives under a temporary
`XDG_CONFIG_HOME` and is removed on exit.

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. Temporary
assertion files and the isolated CLI config directory are removed on exit. Run
`make stop` to stop the foundation stack, or `make reset` to remove local
volumes and start clean.
