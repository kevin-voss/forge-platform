# Authz permission matrix (Forge Identity)

Canonical role and permission matrix for epic 09 (`09.04`). The machine-readable
source of truth is `PermissionMatrix.default()` in
`services/forge-identity`. The JSON fence below must stay in parity with that
code (asserted by `AuthzMatrixTest.publishedDocMatchesCodeMatrix`).

## Roles

| Role | Wire value | Notes |
|---|---|---|
| Organization owner | `organization-owner` | Org-scoped; implies admin on every project in the org |
| Project admin | `project-admin` | Full project control including membership |
| Developer | `developer` | Mutate apps/services/deployments; write secrets/config |
| Viewer | `viewer` | Read-only |
| Service account | `service-account` | Machine role; may deploy / read secrets; cannot manage members |
| None | `none` | No effective membership (deny) |

Human hierarchy: `organization-owner` > `project-admin` > `developer` > `viewer`.
`service-account` is distinct (not in the human hierarchy).

## Decision API

* `POST /v1/authz/check` — `{ principal, project_id, action }` → `{ allow, role, reason }`
* `GET /v1/authz/matrix` — published matrix + version

Deny-by-default: unknown actions and principals with no membership are denied.

## Matrix (version 1)

```json
{
  "version": "1",
  "matrix": {
    "application.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "application.write": [
      "developer",
      "organization-owner",
      "project-admin"
    ],
    "config.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "config.write": [
      "developer",
      "organization-owner",
      "project-admin"
    ],
    "deployment.create": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account"
    ],
    "deployment.delete": [
      "organization-owner",
      "project-admin"
    ],
    "deployment.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "deployment.update": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account"
    ],
    "environment.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "environment.write": [
      "developer",
      "organization-owner",
      "project-admin"
    ],
    "member.manage": [
      "organization-owner",
      "project-admin"
    ],
    "project.delete": [
      "organization-owner",
      "project-admin"
    ],
    "project.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "project.write": [
      "organization-owner",
      "project-admin"
    ],
    "secret.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account"
    ],
    "secret.write": [
      "developer",
      "organization-owner",
      "project-admin"
    ],
    "service.read": [
      "developer",
      "organization-owner",
      "project-admin",
      "service-account",
      "viewer"
    ],
    "service.write": [
      "developer",
      "organization-owner",
      "project-admin"
    ],
    "token.manage": [
      "organization-owner",
      "project-admin"
    ]
  }
}
```
