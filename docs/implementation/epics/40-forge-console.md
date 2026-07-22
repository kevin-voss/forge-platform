# Epic 40: Forge Console

## Status

Planning

## Milestone

**M3 — Global platform.** Third of the six M3 epics (38–43); 43 is the M3 exit capstone.

## Goal

Ship a web interface, `forge-console` (host port `3010`), covering the whole platform: organizations, projects, environments, applications, deployments, nodes, node pools, scaling policies, routes, databases, queues, buckets, secrets metadata, workflows, agents, models, logs, metrics, traces, alerts, incidents, backups, audit history, and costs/usage. When this epic is done, an operator can log in through Identity, browse and search every resource kind, watch deployment status update live, drill into logs/metrics/traces for a service, view an alert/incident, and trigger a rollback — with every single view and action going through a documented public API, never a database. Proven by `demos/40-console`.

## Why this epic exists

By M3 the platform exposes dozens of resource kinds and services, each with its own CLI/curl surface. Operators need one place to see the whole system. Building the Console as a strict API client (rather than a privileged internal service) also forces every capability to have a complete, well-documented public API — if the Console needs data no API exposes, that gap must be fixed in the owning service, not patched around in the UI.

## Relationship to shipped epics

The Console is a client of **epic 09 Forge Identity** (existing auth/session flow — no new auth mechanism), **epic 12 Forge Observe** (existing logs/metrics/traces query API), and every resource-owning service shipped or catalogued so far, reached exclusively through their already-published public HTTP + watch APIs (the SSE watch contract defined by `20-declarative-resource-api`). Compatibility rule: no existing service gains a new internal endpoint "for the Console" — any capability the Console needs that isn't already public must be added as a documented public API on the owning service first (a facade addition), never a private backdoor into that service's database.

## Primary code areas

* `services/forge-console/` — new TypeScript/React SPA (optionally a minimal BFF for session handling only, never for data access)
* `demos/40-console/` — login, live resource browsing, and one write-action acceptance

## Suggested language

TypeScript (React) for the SPA; an optional thin BFF (Node.js) strictly for auth/session cookie handling, itself calling only public Forge APIs.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Console
* No `specs.md` step covers the Console (it postdates the M0 spec); cross-reference every public-API-owning epic it renders: 02, 05, 09, 12, and the M2/M3 catalog

## Dependencies

* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — generic resource CRUD + SSE watch contract every Console view is built on
* Epic [`09-forge-identity`](09-forge-identity.md) — authentication/session flow the Console reuses unmodified
* Epic [`12-forge-observe`](12-forge-observe.md) — logs/metrics/traces query API
* Epics `26`–`39`, `41`, `42` (catalogued, not yet materialized) — each contributes a public read (and sometimes write) API the Console renders; Console features light up incrementally as backing APIs land, never blocking on all of them

## Out of scope for this epic

* Any server-side business logic duplicating a controller's reconciliation — the Console never reimplements platform logic, only renders/calls it
* Direct database access of any kind, for any resource, ever
* A mobile app
* Any write path not already exposed by a public API (no new mutation added solely to satisfy a UI screen)

## Portability contract

The Console has zero provider-specific code paths: it renders whatever `InfrastructureProvider`/`Region`/`NodePool` resources the API returns, so it looks and behaves identically regardless of whether the platform runs on Docker, bare metal, Hetzner, AWS, or Azure. The Console's own deployment never touches the product manifest — it is itself an ordinary Forge-managed workload (`forge apply`-deployed) with no special-cased infrastructure, and its existence never becomes a required dependency for any other epic's gate demo.

```yaml
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: forge-console
  project: platform
  environment: production
spec:
  image: registry.forge.internal/forge/platform/forge-console:1.0.0
  resources: { cpu: 250m, memory: 256Mi }
  scaling: { minReplicas: 1, maxReplicas: 3 }
```

## Success demo

```bash
make demo DEMO=40
```

```text
Log in via Identity (epic 09) session flow
Browse org → project → environment → application tree
Open a demo deployment — status updates live via the watch API, no polling
Drill into logs/metrics/traces for the demo service (epic 12)
View a firing alert and its attached incident (epic 37)
Trigger a rollback from the Console
→ Console calls the same public rollback API a `forge` CLI invocation would
  use — verified by inspecting the network request, not a Console-only path
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 40.01 | Console skeleton + Identity auth | SPA scaffold, session flow, port 3010 |
| 40.02 | Org/project/environment/resource browser | Generic browser over epic 20's resource API |
| 40.03 | Live updates via watch APIs | SSE-driven status without polling |
| 40.04 | Logs/metrics/traces/alerts views | Observe (12) + Alerts (37) query APIs |
| 40.05 | Deployment rollout/rollback actions | Write actions strictly via existing public APIs |
| 40.06 | Databases/queues/buckets/secrets-metadata views | Read views over 28/29/30/31/32's public APIs |
| 40.07 | Costs/usage/audit history views | Read views over epic 41's public API + platform audit log |
| 40.08 | Demo `40-console` + epic gate | Login → live view → rollback acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* The Console never caches or stores platform state beyond the browser session; every page load/refresh re-fetches from the public API.
* Secrets metadata views show names, versions, and rotation status only — never plaintext values, consistent with epic 32's "no plaintext persistence" rule extending to any client, including the Console.
* Features appear incrementally as their backing epics ship; a Console release with, say, no epic 41 yet simply omits the costs tab rather than blocking the whole release.
* The optional BFF, if built, holds no platform data of its own — it exists only to keep session tokens off the client where the auth flow requires it.

## Open questions

* Does the Console call each service's API directly from the browser, or always through the optional BFF? Assumption: direct browser-to-public-API calls where CORS/auth allow it; the BFF is used only where session/cookie handling requires a server-side hop.
* Should the Console support multiple simultaneous organizations in one session? Assumption: yes, an org switcher backed by the same Identity session, no re-login required.
* How does the Console degrade when a backing service (e.g. Alerts) is down? Assumption: that section shows a documented "unavailable" state; the rest of the Console keeps working, since it is a client with no shared failure domain across services.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **40.01 — Console skeleton + Identity auth** first: no resource browser or view can be built before the SPA scaffold and login flow exist.
