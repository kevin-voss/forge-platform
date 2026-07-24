# Platform findings

Single, living record of **platform bugs and contract mismatches** surfaced by the demo-project
E2E track. Populated while running the demos (epics 51–55) and consolidated by epic
[`56.03`](../implementation/steps/56-platform-e2e-gate/56.03-findings-consolidation.md).

**Rules**
* One entry per finding, using [`findings-template.md`](findings-template.md). Append-only; never
  edit a demo's *service* to make a finding disappear — fixing the platform is separate work.
* Only genuine **platform** issues go here. Demo/app/manifest/test bugs are fixed in the demo.
* The harness (`tests/e2e/harness/findings.ts`) is the automated writer; humans may add entries too.

Machine-readable mirror: `tests/e2e/artifacts/findings.json`.

---

## Summary

| Metric | Count |
|---|---|
| Total findings | 4 |
| Open | 4 |
| Blocker | 0 |
| Major | 2 |
| Minor | 2 |

## By service

| Service | Open | Blocker | Major | Minor |
|---|--:|--:|--:|--:|
| forge-identity | 1 | 0 | 0 | 1 |
| forge-observe | 1 | 0 | 1 | 0 |
| forge-secrets / forge-control | 1 | 0 | 0 | 1 |
| platform | 1 | 0 | 1 | 0 |

## By demo

| Demo | Findings |
|---|--:|
| 01-taskflow | 4 |
| 02-snapnote | 0 |
| 03-askdocs | 0 |
| 04-orderpipe | 0 |
| 05-pulseboard | 0 |

---

## Findings

### F-001 — No prescribed app JWT-over-PAT product session pattern

| Field | Value |
|---|---|
| Status | Open |
| Severity | minor |
| Service | forge-identity |
| Area / contract | Product auth guidance / OpenAPI sessions+tokens (epic 09) |
| Found by demo | 01-taskflow (step 51.03) |
| First seen | 2026-07-24 |
| Reproducible | always |

**What we tested**
TaskFlow needs a browser-facing login that yields a client-stored credential while API
authorization remains Identity introspect (PAT/session). Product design asks for an
app-issued JWT wrapping a PAT.

**Expected (per spec/contract)**
A platform-prescribed login/session pattern for product apps (or explicit guidance that
apps should mint their own JWT over Identity PATs).

**Actual**
Identity exposes register/login → opaque `session_token`, plus PAT issue/introspect, but
does not define an app JWT / product-session contract. TaskFlow adopts PAT-as-Bearer with
an optional local HS256 JWT (`JWT_SIGNING_KEY` injected from Forge Secrets as of 51.04)
and local `admin`/`member` roles in product Postgres.

**Evidence**
- Identity OpenAPI: `POST /v1/auth/login`, `POST /v1/auth/introspect`, `POST /v1/tokens`
- No app-JWT issuance endpoint or product session schema in forge-identity

**Impact**
Demo/apps must invent thin JWT wrapping; roles like `admin`/`member` stay product-local
(distinct from platform `developer`/`viewer`).

**Workaround used by demo**
TaskFlow API mints PAT on signup/login, returns PAT (+ optional JWT embedding the PAT);
middleware always introspects the PAT via Identity.

**Suggested platform fix**
Document the recommended product auth pattern (PAT-as-Bearer vs app JWT) in Identity docs /
contracts, or add a first-class product-session helper if the platform wants to own it.

### F-002 — Application `valueFrom.secret` is documentation-only; slash secret names rejected

| Field | Value |
|---|---|
| Status | Open |
| Severity | minor |
| Service | forge-secrets / forge-control |
| Area / contract | Portable Application env + Secrets name/bindings (epics 10, 20) |
| Found by demo | 01-taskflow (step 51.04) |
| First seen | 2026-07-24 |
| Reproducible | always |

**What we tested**
Product design asks for Application env entries like
`valueFrom: { secret: taskflow/db-url }` and `taskflow/jwt-key`, expecting apply to wire
injection from Forge Secrets.

**Expected (per product design)**
`forge apply` materialises `valueFrom.secret` into Secrets bindings (or equivalent), and
slash-namespaced secret ids are accepted.

**Actual**
- Secret / binding names must match `[A-Za-z_][A-Za-z0-9_]*` (slashes rejected).
- Apply stores Application `spec.env` / `valueFrom` as opaque JSON but does **not** create
  Secrets bindings; injection requires explicit `forge secret set` +
  `PUT …/services/{svc}/bindings` (and managed-db attach for `DATABASE_URL`).

**Evidence**
- `tools/forge-cli/cmd/secret.go` `secretNamePattern`
- `services/forge-secrets` bindings `validate_env_name`
- Control `ApplyService` / `PortableManifest` accept env without binding side effects

**Impact**
Demo/apps must provision secrets + bindings in `run.sh` (or equivalent) even when the
portable manifest already declares `valueFrom.secret` refs.

**Workaround used by demo**
TaskFlow documents `valueFrom` refs in `forge.yaml` using env-var names
(`DATABASE_URL`, `JWT_SIGNING_KEY`); `run.sh` sets the JWT secret, puts bindings on
service `api`, and relies on managed-db attach for `DATABASE_URL`.

**Suggested platform fix**
Either wire apply/`valueFrom.secret` → Secrets bindings, or document that bindings are
mandatory and restrict product design to valid secret name grammar (no `/`).

### F-003 — Observe should record at least one trace for POST /tasks

| Field | Value |
|---|---|
| Status | Open |
| Severity | major |
| Service | forge-observe |
| Area / contract | forge-observe / product OTEL export (51.05) |
| Found by demo | 01-taskflow |
| First seen | 2026-07-24 |
| Reproducible | always |

**What we tested**
POST /tasks then query Tempo /api/search and Observe /v1/logs

**Expected (per spec/contract)**
≥1 OTEL trace (or observe log evidence) for POST /tasks

**Actual**
no OTEL trace evidence for POST /tasks (tempo search returned zero traces; observe HTTP 400)

**Evidence**
- _(none captured)_

**Reproduce**
```bash
make demo DEMO=51 KEEP=1
curl -s "http://127.0.0.1:3002/api/search?limit=20"
curl -s "http://127.0.0.1:4106/v1/logs?limit=50"
```

**Impact on demo**
Demo marked **degraded**; run continues.

### F-004 — Managed Postgres task data must survive API container restart

| Field | Value |
|---|---|
| Status | Open |
| Severity | major |
| Service | platform |
| Area / contract | managed PostgreSQL durability (51.02/51.05) |
| Found by demo | 01-taskflow |
| First seen | 2026-07-24 |
| Reproducible | intermittent |

**What we tested**
create+complete Buy milk, docker restart API container, GET /tasks

**Expected (per spec/contract)**
same task id/title/done=true still present via managed Database

**Actual**
After `docker restart` of the API container, Gateway sometimes returns HTTP 502
`upstream connection error` on member login / `/tasks` before the upstream is healthy
again (race vs readiness). Example:
`{"error":{"code":"bad_gateway","message":"upstream connection error","requestId":"req_2cdbb3ecb61d0ad1327439d1b5980273"}}`

**Evidence**
- _(none captured)_

**Reproduce**
```bash
make demo DEMO=51 KEEP=1
docker restart $(docker ps -q --filter label=forge.managed=true | head -1)
curl -H Host:api.taskflow.localhost -H "Authorization: Bearer $PAT" http://127.0.0.1:4000/tasks
```

**Impact on demo**
Demo marked **degraded**; run continues.
