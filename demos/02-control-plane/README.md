# Demo 02: Forge Control

An end-to-end acceptance gate for the Forge Control desired-state API. It starts
PostgreSQL and Control, verifies migrations, then creates and reads the complete
project hierarchy:

```text
project
├── environment (development)
└── application (web)
    └── service (api :8080)
        └── desired deployment (localhost:5000/demo-go:latest)
```

## What this demo checks

* Control starts with `DATABASE_MIGRATE_ON_START=true`; the Flyway schema history
  and `control.deployments` table exist before API requests run.
* The HTTP create chain succeeds and every returned resource identifier is a UUID.
* `GET /v1/projects/{id}?expand=tree` returns the complete hierarchy with the
  created IDs and desired deployment values.
* An unknown project returns `404` with the canonical
  `{"error":{"code","message","requestId"}}` envelope.
* A Control restart preserves the hierarchy and returns the identical tree.
* The service image builds, readiness succeeds, and the script exits non-zero
  with the last Control logs if any check fails.

Control records desired state only. The demo does not pull or run the illustrative
deployment image; workload execution is introduced in epic 04.

## Run

From the repository root:

```bash
make demo DEMO=02
```

The demo rebuilds and recreates `forge-control`, starts `forge-postgres` if
needed, and stops Control through its exit trap. PostgreSQL remains available for
the foundation stack. It creates uniquely named resources in the shared `control`
schema so repeated runs do not collide.

To inspect the state afterwards:

```bash
docker compose logs forge-control
curl -sf http://127.0.0.1:4001/health/ready
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control endpoint used by the checks |
| `FORGE_AUTH_MODE` | `dev` (explicit in `run.sh`) | Insecure bypass; Control defaults to `enforce` as of `09.06` |

No secrets are stored in this demo. The `localhost:5000/demo-go:latest` image
reference is data only and is not pulled.

## Prerequisites and cleanup

Docker, Docker Compose, `curl`, and `python3` are required. The root Compose
configuration supplies PostgreSQL credentials and enables automatic migrations.
Temporary assertion files are removed on exit. Run `make stop` to stop the
foundation stack, or `make reset` to remove local volumes and start clean.
