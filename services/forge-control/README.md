# forge-control

Central control-plane API for Forge Platform (Kotlin + Ktor).

Persistence (`02.02`) provides the Control domain model — projects, environments,
applications, services, deployments, and an append-only audit log — in PostgreSQL
schema `control`, with Flyway migrations, HikariCP, and JDBC repositories.

HTTP APIs for projects and environments (`02.03`) are available under `/v1`.
Applications, services, and deployments arrive in later steps (`02.04+`).

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-control
curl -sf http://127.0.0.1:4001/health/live
curl -sf http://127.0.0.1:4001/health/ready
```

Create a project and environment:

```bash
PID=$(curl -sf -X POST http://127.0.0.1:4001/v1/projects \
  -H 'content-type: application/json' -d '{"name":"acme"}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf http://127.0.0.1:4001/v1/projects/$PID
curl -sf -X POST http://127.0.0.1:4001/v1/projects/$PID/environments \
  -H 'content-type: application/json' -d '{"name":"development"}'
curl -sf http://127.0.0.1:4001/v1/projects/$PID/environments
```

Or from this directory:

```bash
make migrate   # apply schema without starting HTTP
make run
make test
```

Local JVM (no Docker for the service; needs foundation Postgres on `:5001`):

```bash
make migrate
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4001:8080`. Wins over `FORGE_HTTP_PORT`. |
| `FORGE_SERVICE_NAME` | `forge-control` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Auth bypass until Identity `09.06`; creates attributed to actor `dev` |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |
| `DATABASE_URL` | `jdbc:postgresql://127.0.0.1:5001/forge` (local) / `…://postgres:5432/forge` (Compose) | JDBC URL |
| `DATABASE_USER` | `forge` | |
| `DATABASE_PASSWORD` | `forge` | Never logged |
| `DATABASE_SCHEMA` | `control` | Flyway default schema |
| `DATABASE_POOL_MAX` | `10` | HikariCP max pool size |
| `DATABASE_MIGRATE_ON_START` | `true` | Run Flyway on boot |

See `.env.example`.

## HTTP API (02.03)

| Method | Path | Notes |
|---|---|---|
| `POST` | `/v1/projects` | Body `{"name","slug?"}`; slug derived from name when omitted |
| `GET` | `/v1/projects` | List projects |
| `GET` | `/v1/projects/{projectId}` | Get project |
| `POST` | `/v1/projects/{projectId}/environments` | Body `{"name"}` |
| `GET` | `/v1/projects/{projectId}/environments` | List environments for project |
| `GET` | `/v1/environments/{environmentId}` | Get environment |

Errors use the provisional envelope `{"error":{"code","message","details?"}}`
(`400` validation, `404` missing, `409` conflict). Formalized in `02.06`.

## Schema

Tables in schema `control`:

* `projects`, `environments`, `applications`, `services`, `deployments`
* `audit_log` (append-only; create actions for projects/environments)
* `flyway_schema_history`

Foreign keys use `ON DELETE RESTRICT`. Unique constraints enforce slug/name uniqueness
within parents. Check constraints cover non-blank names, `port` 1–65535, and
`desired_replicas >= 0`.

## Health

* `GET /health/live` → `200 {"status":"live"}`
* `GET /health/ready` → `200 {"status":"ready"}` once started **and** `SELECT 1` succeeds; `503` otherwise

## Makefile targets

```bash
make migrate        # Flyway only (no HTTP server)
make dev            # run with Gradle on localhost
make build          # jar
make run            # Compose up on :4001
make test           # unit + integration
make test-unit
make test-integration
make docker-build
make clean
```
