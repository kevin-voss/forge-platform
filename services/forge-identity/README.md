# forge-identity

Kotlin/Ktor identity service for Forge Platform (epic 09). Host port **4002**.

## Current scope (through 09.04)

* Runtime-contract health: `GET /health/live`, `GET /health/ready`
* Service identity: `GET /` → `{ service, language, status }`
* Tenancy APIs: users, organizations, projects (Control id link), org/project memberships
* Auth APIs: register / login / introspect / logout (Argon2id + opaque sessions)
* Authz APIs: `POST /v1/authz/check`, `GET /v1/authz/matrix` (role model + permission matrix)
* Postgres database `forge_identity` + Flyway migrations (`V09_01`–`V09_03`)
* Shared error envelope (`code`, `message`, `details`, `requestId`)
* Structured JSON logs, env config, SIGTERM graceful shutdown
* Compose service + OpenAPI

API tokens / service-account issuance and Control enforcement arrive in later 09.x steps.

## Local commands

```bash
# Unit tests (AuthTest/TenancyTest/AuthzMatrixTest need foundation Postgres on :5001)
make -C services/forge-identity test-unit

# Compose run (builds image, waits for live+ready)
make -C services/forge-identity run

# Full unit + integration (requires Docker/Postgres)
make -C services/forge-identity test
```

## Authz smoke

```bash
make -C services/forge-identity run
# create org/project + developer/viewer memberships, then:
curl -s -X POST localhost:4002/v1/authz/check \
  -d '{"principal":{"type":"user","id":"<dev>"},"project_id":"<prj>","action":"deployment.create"}' \
  -H 'content-type: application/json' | jq .
curl -s localhost:4002/v1/authz/matrix | jq '.matrix["deployment.create"]'
```

Human matrix doc: [`docs/contracts/authz-permission-matrix.md`](../../docs/contracts/authz-permission-matrix.md).

## Auth smoke

```bash
make -C services/forge-identity run
curl -s -X POST localhost:4002/v1/auth/register \
  -d '{"email":"a@x.com","password":"s3cret!!","display_name":"A"}' \
  -H 'content-type: application/json'
TOK=$(curl -s -X POST localhost:4002/v1/auth/login \
  -d '{"email":"a@x.com","password":"s3cret!!"}' \
  -H 'content-type: application/json' | jq -r .session_token)
curl -s -X POST localhost:4002/v1/auth/introspect \
  -d "{\"token\":\"$TOK\"}" -H 'content-type: application/json' | jq '{active,user_id}'
curl -s -X POST localhost:4002/v1/auth/logout \
  -H "Authorization: Bearer $TOK" -o /dev/null -w '%{http_code}\n'
curl -s -X POST localhost:4002/v1/auth/introspect \
  -d "{\"token\":\"$TOK\"}" -H 'content-type: application/json' | jq .active
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
| `FORGE_SESSION_TTL_S` | `86400` | Fixed session lifetime (seconds) |
| `FORGE_ARGON2_MEMORY_KB` | `65536` | Argon2id memory |
| `FORGE_ARGON2_ITERATIONS` | `3` | Argon2id iterations |
| `FORGE_LOGIN_MAX_FAILS` | `5` | Lockout threshold (per 15 min window) |
| `FORGE_AUTHZ_MATRIX_VERSION` | `1` | Informational; matrix defined in code |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

TLS is terminated upstream (Gateway) for local development.
