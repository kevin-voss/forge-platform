# Demo 09: Platform Identity

End-to-end acceptance gate for epic 09 (Forge Identity). The script brings up
PostgreSQL, the local registry, Identity, Control in **enforce** mode, and
Runtime; then proves authentication, project roles, and token revocation against
the real secured stack:

```text
1 create user            2 create org            3 create project
4 issue developer token  5 deploy (developer)=201
6 deploy (viewer)=403    7 revoke developer token
8 deploy (revoked)=401   → PASS
```

```text
forge login / CLI ──Bearer──► Forge Control (FORGE_AUTH_MODE=enforce)
                                   │
                                   ▼ introspect + authz/check
                              Forge Identity
                                   │
Control (authorized) ─────────────► Forge Runtime → demo-go container
```

## What this demo checks

* A registered user can create an org and a Control project; the project id is
  registered in Identity (shared id).
* Developer API tokens authorize `deployment.create` (`201`).
* Viewer tokens are denied (`403` / `forbidden`).
* Revoked developer tokens are rejected after the short introspection cache TTL
  (`401` / `unauthenticated`).
* Unauthenticated deploy requests are rejected (`401`).
* Identity and Control logs contain no plaintext passwords or token secrets.
* The deployed image is the epic-01 Go demo (`PORT`, `/`, `/health/live`,
  `/health/ready`).

**This demo never sets `FORGE_AUTH_MODE=dev`.** Enforce mode is the security
acceptance. Test passwords and tokens are ephemeral and must not be reused.

## Run

From the repository root:

```bash
make demo DEMO=09
```

Expect a final `demo 09 PASSED` line and exit code `0`:

```text
[4] developer token issued
[5] deploy as developer -> 201 OK
[6] deploy as viewer -> 403 OK
[7] revoked developer token
[8] deploy with revoked token -> 401 OK
logs contain plaintext secret: no
demo 09 PASSED
```

Optional phase split (CI targeting):

```bash
./demos/09-platform-identity/run.sh --phase=bootstrap
./demos/09-platform-identity/run.sh --phase=identity
./demos/09-platform-identity/run.sh --phase=authz
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API + readiness |
| `FORGE_IDENTITY_HOST_URL` | `http://127.0.0.1:4002` | Host Identity API + `forge login` (Control uses in-network URL) |
| `FORGE_RUNTIME_HOST_URL` | `http://127.0.0.1:4102` | Runtime readiness |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_PROFILE` | `demo` | Isolated CLI profile name |
| `FORGE_AUTH_MODE` | `enforce` (forced in `run.sh`) | Must remain enforce |
| `FORGE_INTROSPECT_CACHE_TTL_S` | `2` | Short TTL so revoke is prompt |
| `FORGE_AUTHZ_CACHE_TTL_S` | `2` | Short authz cache for the demo |
| `DEMO_IMAGE` | `localhost:5000/demo-go:identity` | Contract-compliant deploy target |
| `FORGE_LIFECYCLE_OWNER` | `control` | Control creates workloads |

`docker-compose.yml` in this directory is an overlay on the root `compose.yaml`
that pins Control to enforce mode, short cache TTLs, and `depends_on`
`forge-identity`.

CLI credentials live under a temporary `XDG_CONFIG_HOME` removed on exit
(`forge logout` is also attempted).

## Prerequisites and cleanup

Docker, Docker Compose, Go, `curl`, `make`, and `python3` are required. On exit
the script clears the CLI profile, stops Identity / Control / Runtime, and
removes managed demo containers. PostgreSQL and the registry may remain for the
foundation stack. Use `make stop` or `make reset` for a full teardown.
