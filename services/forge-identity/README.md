# forge-identity

Kotlin/Ktor identity service for Forge Platform (epic 09). Host port **4002**.

## Step 09.01 scope

* Runtime-contract health: `GET /health/live`, `GET /health/ready`
* Service identity: `GET /` → `{ service, language, status }`
* Postgres database `forge_identity` + Flyway baseline migration
* Structured JSON logs, env config, SIGTERM graceful shutdown
* Compose service + skeleton OpenAPI

Domain APIs (users, sessions, tokens, authz) arrive in later 09.x steps.

## Local commands

```bash
# Unit tests
make -C services/forge-identity test-unit

# Compose run (builds image, waits for live+ready)
make -C services/forge-identity run

# Full unit + integration (requires Docker/Postgres)
make -C services/forge-identity test
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `4002` (host) / `8080` (container) | Listen port |
| `FORGE_SERVICE_NAME` | `forge-identity` | Identity payload + logs |
| `FORGE_LOG_LEVEL` | `info` | debug\|info\|warn\|error |
| `FORGE_IDENTITY_DB_URL` | *(required)* | e.g. `jdbc:postgresql://postgres:5432/forge_identity` |
| `FORGE_IDENTITY_DB_USER` | `forge` | Env only; never logged |
| `FORGE_IDENTITY_DB_PASSWORD` | `forge` | Env only; never logged |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

TLS is terminated upstream (Gateway) for local development.
