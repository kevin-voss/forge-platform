# forge-identity

Kotlin/Ktor identity service for Forge Platform (epic 09). Host port **4002**.

## Current scope (through 09.05)

* Runtime-contract health: `GET /health/live`, `GET /health/ready`
* Service identity: `GET /` → `{ service, language, status }`
* Tenancy APIs: users, organizations, projects (Control id link), org/project memberships
* Auth APIs: register / login / introspect / logout (Argon2id + opaque sessions)
* Authz APIs: `POST /v1/authz/check`, `GET /v1/authz/matrix` (role model + permission matrix)
* Machine identity: service accounts, API tokens (`forge_pat_` / `forge_sat_`), revocation
* Introspection accepts sessions **or** API tokens (superset response with `principal_id` / `project_id` / `role`)
* Postgres database `forge_identity` + Flyway migrations (`V09_01`–`V09_05`)
* Shared error envelope (`code`, `message`, `details`, `requestId`)
* Structured JSON logs, env config, SIGTERM graceful shutdown
* Compose service + OpenAPI

Control enforcement of tokens arrives in 09.06.

## Local commands

```bash
# Unit tests (AuthTest/TenancyTest/AuthzMatrixTest/TokenTest need foundation Postgres on :5001)
make -C services/forge-identity test-unit

# Compose run (builds image, waits for live+ready)
make -C services/forge-identity run

# Full unit + integration (requires Docker/Postgres)
make -C services/forge-identity test
```

## Token smoke

```bash
make -C services/forge-identity run
# create a developer token for a user in prj_1 (after seeding user/project membership)
T=$(curl -s -X POST localhost:4002/v1/tokens \
  -d '{"owner":{"type":"user","id":"usr_dev"},"project_id":"prj_1","role":"developer"}' \
  -H 'content-type: application/json')
TOK=$(echo "$T" | jq -r .token); TID=$(echo "$T" | jq -r .token_id)
curl -s -X POST localhost:4002/v1/auth/introspect -d "{\"token\":\"$TOK\"}" -H 'content-type: application/json' | jq '{active,role,project_id}'
curl -s -X POST localhost:4002/v1/tokens/$TID/revoke -o /dev/null -w '%{http_code}\n'
curl -s -X POST localhost:4002/v1/auth/introspect -d "{\"token\":\"$TOK\"}" -H 'content-type: application/json' | jq .active
curl -s "localhost:4002/v1/tokens?owner=usr_dev" | jq '.[0] | has("token")'
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
| `FORGE_TOKEN_DEFAULT_TTL_S` | *(none)* | Default API token TTL; unset = no expiry |
| `FORGE_TOKEN_PREFIX_LEN` | `8` | Identification prefix length retained after create |
| `FORGE_AUTHZ_MATRIX_VERSION` | `1` | Informational; matrix defined in code |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | SIGTERM drain window |

TLS is terminated upstream (Gateway) for local development.
