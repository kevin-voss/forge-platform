# Epic 51: Demo 1 — TaskFlow (auth + database + secrets baseline)

## Status

In progress

## Goal

A small team task-manager product that deploys onto Forge and proves the core developer path:
build from source, get a **managed Postgres** database, inject **secrets**, protect the app with
**Identity** (login, PATs, roles), and serve it through the **Gateway** — verified by a headed
browser E2E of signup → login → create/complete tasks → role gating.

## Why this epic exists

Every SaaS needs auth + a database + secrets + a route. TaskFlow is the smallest believable product
exercising all of them together, making it the reference for the platform's "boring but essential"
path. Full design: [`../../demo-projects/projects/01-taskflow.md`](../../demo-projects/projects/01-taskflow.md).

## Primary code areas

* `demos/51-taskflow/` — product source (Go API + minimal SPA), Dockerfiles, `forge.yaml`, resource
  docs, `run.sh`, `seed.sh`, `demo.json`.
* `tests/e2e/projects/01-taskflow/spec.ts` — browser E2E.

## Suggested language

Go (API) + minimal static SPA (shared harness style). Reuses platform services; no new services.

## Spec references

* `docs/demo-projects/projects/01-taskflow.md`
* Epics 06 (build), 09 (identity), 10 (secrets), 18 (managed Postgres), 05 (gateway), 20 (apply).

## Dependencies

* Epic **50** (harness) complete.
* Identity, Secrets, managed Postgres, Gateway, Build available.

## Out of scope for this epic

* Events, storage, AI, autoscaling (covered by other demos).
* Password reset / email flows (auth is proven via PAT + app JWT only).

## Success demo

`make demo DEMO=51` deploys TaskFlow to Ready; `01-taskflow` E2E signs up a user, logs in, creates
and completes a task that survives a restart, and shows admin-only controls hidden from members;
deploy with a viewer PAT returns 403 while a developer PAT succeeds.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 51.01 | Product scaffold + baseline deploy | Complete | Go API + SPA, Dockerfiles, `forge build` + `forge apply`, routes app./api.taskflow.localhost |
| 51.02 | Managed Postgres + schema | Complete | `dependencies.database`; migrations; users/projects/tasks; seed.sh |
| 51.03 | Identity auth + roles | Complete | signup/login → PAT; introspect middleware; admin/member gating; deploy RBAC viewer=403/developer=200 |
| 51.04 | Secrets injection | Complete | DB url + JWT key from forge-secrets; no plaintext in manifest/logs |
| 51.05 | E2E browser spec | Complete | signup→login→create→persist→complete→role gating; product + platform assertions |
| 51.06 | Demo + epic gate | Not started | `demos/51-taskflow` run/seed/demo.json; `make demo DEMO=51`; wired into test-platform-e2e |

Ordering + `N`: [`../steps/51-demo-taskflow/README.md`](../steps/51-demo-taskflow/README.md).

## Open questions

* ~~Does the platform prescribe a login/session pattern, or is app-issued JWT-over-PAT acceptable?~~
  **Resolved in 51.03:** no prescribed app JWT pattern — recorded as finding `F-001`; TaskFlow uses
  PAT-as-Bearer (+ optional local JWT) with product-local `admin`/`member` roles.
