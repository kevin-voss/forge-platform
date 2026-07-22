# forge-identity

Kotlin/Ktor identity service for Forge Platform (epic 09). Host port **4002**.

## Current scope (through 09.02)

* Runtime-contract health: `GET /health/live`, `GET /health/ready`
* Service identity: `GET /` → `{ service, language, status }`
* Tenancy APIs: users, organizations, projects (Control id link), org/project memberships
* Postgres database `forge_identity` + Flyway migrations (`V09_01`, `V09_02`)
* Shared error envelope (`code`, `message`, `details`, `requestId`)
* Structured JSON logs, env config, SIGTERM graceful shutdown
* Compose service + OpenAPI

Auth (passwords/sessions), role enforcement, and tokens arrive in later 09.x steps.

## Local commands

```bash
# Unit tests (TenancyTest needs foundation Postgres on :5001)
make -C services/forge-identity test-unit

# Compose run (builds image, waits for live+ready)
make -C services/forge-identity run

# Full unit + integration (requires Docker/Postgres)
make -C services/forge-identity test
```

## Tenancy smoke

```bash
make -C services/forge-identity run
U=$(curl -s -X POST localhost:4002/v1/users \
  -d '{"email":"dev@x.com","display_name":"Dev"}' \
  -H 'content-type: application/json' | jq -r .id)
O=$(curl -s -X POST localhost:4002/v1/orgs \
  -d '{"name":"Acme"}' \
  -H 'content-type: application/json' | jq -r .id)
curl -s -X POST "localhost:4002/v1/orgs/$O/members" \
  -d "{\"user_id\":\"$U\",\"role\":\"organization-owner\"}" \
  -H 'content-type: application/json'
curl -s "localhost:4002/v1/users/$U/memberships" | jq
# duplicate email → 409
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:4002/v1/users \
  -d '{"email":"dev@x.com","display_name":"Dup"}' \
  -H 'content-type: application/json'
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
| `FORGE_IDENTITY_SEED_ADMIN` | *(optional)* | Bootstrap admin email on first start |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

TLS is terminated upstream (Gateway) for local development.
