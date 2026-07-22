# Demo 06: Source-to-deployment (Forge Build)

End-to-end acceptance gate for Forge Build. The script brings up PostgreSQL, the
local OCI registry, Control, Runtime, Gateway, and Build; initializes a local
fixture Git repository; then proves a commit can become a running, routed
service:

```text
fixture Git repo
      │
      ▼
Forge Build (clone → docker build → push)
      │
      ▼
localhost:5000/<project>-api:<shortsha>-<buildid>
      │
      ▼
Control (image recorded on service + autoDeploy)
      │
      ▼
Runtime (converge container) → Gateway → api.demo.localhost
```

## What this demo checks

* A fixture commit is built via `POST /v1/builds` with streamed logs available
  during and after the build.
* The image is pushed to `localhost:5000` with a tag encoding commit SHA + build
  id, and recorded on the Control service.
* `autoDeploy: true` creates a Control deployment; Runtime converges it to
  `active`; Gateway routes `api.demo.localhost` with HTTP `200`.
* A broken Dockerfile build ends `failed`, exposes no image, creates no
  deployment, and removes its temporary workspace.

This pre-09 demo sets `FORGE_AUTH_MODE=dev` explicitly (Control defaults to
`enforce` as of `09.06`). Build and Runtime mount the host Docker socket — a
privileged local-dev convenience, not for production.

## Fixture repos

| Path | Mount inside Build | Purpose |
|---|---|---|
| `fixture/` | `/fixtures/demo` | Happy-path Python HTTP app + `Dockerfile` + `forge.yaml` |
| `fixture-broken/` | `/fixtures/demo-broken` | Dockerfile that fails (`RUN false`) |

`run.sh` initializes (or refreshes) a local Git commit in each fixture directory
before submitting builds. No remote Git or registry auth is required.

## Run

From the repository root:

```bash
make demo DEMO=06
```

Expect a final `Demo 06 passed.` line and exit code `0`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_BUILD_URL` | `http://127.0.0.1:4103` | Build API |
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness + service image checks |
| `FORGE_RUNTIME_URL` | `http://127.0.0.1:4102` | Runtime readiness |
| `FORGE_GATEWAY_URL` | `http://127.0.0.1:4000` | Gateway health, routes, data-plane curls |
| `FORGE_REGISTRY` | `localhost:5000` | Local OCI registry |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `FORGE_HOST_PATTERN` | `{service}.demo.localhost` | Hostname template (set by `run.sh`) |
| `FORGE_AUTH_MODE` | `dev` (explicit in `run.sh`) | Insecure bypass; Control defaults to `enforce` as of `09.06` |

No secrets are stored. CLI config lives under a temporary `XDG_CONFIG_HOME`
removed on exit. Gateway curls use `curl -H 'Host: api.demo.localhost'` so no
DNS setup is required.

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `git`, `make`, and `python3` are required.
On exit the script deletes demo deployments, removes managed containers and
pulled demo images, and stops Gateway, Runtime, Build, and Control. PostgreSQL
and the registry may remain for the foundation stack. Use `make stop` or
`make reset` for a full teardown.
