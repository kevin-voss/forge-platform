# Epic 09: Forge Identity

## Status

In progress

## Goal

Stand up Forge Identity — a Kotlin/Ktor service on port `4002` — that owns platform users, organizations, projects, memberships, roles, sessions, API tokens, and service accounts, and makes Forge Control **enforce authorization** on mutations. When this epic is done, unauthenticated platform changes are rejected, project roles (`organization-owner`, `project-admin`, `developer`, `viewer`, `service-account`) are enforced, service accounts work without human sessions, revoked tokens stop working, secrets/passwords are never logged, and `forge login` stores a token profile the CLI uses. The dev auth bypass (`FORGE_AUTH_MODE=dev`) used since epic 02 is removed from the default path. Proven by `demos/09-platform-identity`.

## Why this epic exists

Epics 02–08 deliberately ran with a documented local-dev auth bypass so the control/runtime/scheduler machinery could be built first. That is not shippable: anyone can mutate any project. Identity introduces authentication, multi-tenant isolation, and role-based authorization — the foundation every later product-facing feature (secrets scoping, storage isolation, agent permissions) depends on.

## Primary code areas

* `services/forge-identity/` — new Kotlin/Ktor service (users, orgs, projects, roles, tokens, sessions), Postgres schema
* `services/forge-control/` — authz middleware calling Identity to authenticate + authorize mutations (`09.06`)
* `tools/forge-cli/` — `forge login` + token profile storage (`09.07`)
* `demos/09-platform-identity/` — full identity acceptance scenario
* `contracts/openapi/` — Identity API + Control authz error contract

## Suggested language

Kotlin with Ktor (per `specs.md` Step 09). Password hashing via a vetted algorithm (Argon2id or bcrypt); sessions + API tokens are opaque, hashed at rest.

## Spec references

* `specs.md` → Step 09: Forge Identity (registration, login, sessions, orgs, projects/memberships, roles, API tokens, service accounts, revocation, audit events)
* `specs.md` → Step 02 (Control mutations that must become authorized)
* `docs/implementation/MASTER_PLAN.md` → Epic 09 catalog + port `4002` + auth-bypass assumption

## Dependencies

* Epic [`02-forge-control`](02-forge-control.md) — the mutations that become authorized; project/app/service model
* Epic [`03-forge-cli`](03-forge-cli.md) — CLI profiles/config to store the login token (`03.01`)
* Foundation `00` — Postgres, Compose, OTEL

## Out of scope for this epic

* SSO / OAuth / OIDC federation (local username+password + tokens only)
* Fine-grained per-resource ACLs beyond project roles (richer ACLs arrive where needed, e.g. memory `17.04`)
* Gateway-level authentication enforcement (optional; Control enforcement is the gate here)
* MFA, password reset email flows, account recovery
* Billing / quotas

## Success demo

```bash
make demo DEMO=09
```

```text
1. create user            → 201
2. create organization    → owner membership
3. create project         → within org
4. issue developer token  → deploys succeed
5. deploy application      → 200 (authorized)
6. viewer token deploy     → 403 (role enforced)
7. revoke developer token  → subsequent calls 401
8. verify access fails     → revoked token unusable
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [09.01](../steps/09-forge-identity/09.01-skeleton-compose-postgres.md) | Skeleton + Compose + Postgres | Complete | Ktor service on 4002, health, schema home |
| [09.02](../steps/09-forge-identity/09.02-users-orgs-memberships.md) | Users, orgs, memberships persistence | Complete | Core tenancy model |
| [09.03](../steps/09-forge-identity/09.03-registration-login-sessions.md) | Registration, login, sessions | Complete | Password hashing + session lifecycle |
| [09.04](../steps/09-forge-identity/09.04-roles-and-project-membership.md) | Roles + project membership | Complete | RBAC model + permission evaluation |
| [09.05](../steps/09-forge-identity/09.05-api-tokens-service-accounts-revocation.md) | API tokens + service accounts + revocation | Complete | Machine identity + revoke |
| [09.06](../steps/09-forge-identity/09.06-control-authz-middleware.md) | Control authz middleware | Not started | End `FORGE_AUTH_MODE=dev` default |
| [09.07](../steps/09-forge-identity/09.07-cli-login-and-token-profile.md) | CLI `forge login` + token profile | Not started | Store + use token |
| [09.08](../steps/09-forge-identity/09.08-demo-09-platform-identity.md) | Demo `09-platform-identity` + gate | Not started | Full scenario; epic gate |

## Assumptions

* Authentication credentials are local: username/email + password for humans; opaque API tokens for machines. No external IdP in this epic.
* Sessions are opaque tokens (server-side session records) with expiry; API tokens are long-lived until revoked, stored hashed (only a prefix shown after creation).
* Control authorizes by calling Identity's introspection endpoint (`POST /v1/auth/introspect`) with the presented token, receiving principal + roles, and evaluating a permission matrix locally. Introspection results are cached briefly (default 10s) with revocation honored on cache miss.
* The permission model is role-per-project (plus org-owner). The matrix maps `(role, action)` → allow/deny; actions map to Control mutation categories.
* `FORGE_AUTH_MODE=dev` remains available as an explicit opt-in for other demos but is **not** the default after `09.06`; default becomes `enforce`.
* Passwords hashed with Argon2id (fallback bcrypt); never logged; token values never logged (only prefixes/ids).

## Open questions

* Introspection (Control calls Identity per request, cached) vs signed JWTs (stateless, harder to revoke). Assumption: opaque tokens + cached introspection for immediate revocation; revisit if latency demands JWTs.
* Should the Gateway also authenticate? Assumption: optional, out of scope here; Control enforcement satisfies the gate.
* Where do audit events live — Identity DB now, Events service later (epic 11)? Assumption: Identity-local `audit_events` table now; optionally forwarded to Events later.
* Org vs project hierarchy depth — single org→projects, or nested? Assumption: flat org → projects (matches `specs.md`).

## Next step to implement

**[09.06](../steps/09-forge-identity/09.06-control-authz-middleware.md) — Control authz middleware**.
