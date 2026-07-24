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
| Total findings | 1 |
| Open | 1 |
| Blocker | 0 |
| Major | 0 |
| Minor | 1 |

## By service

| Service | Open | Blocker | Major | Minor |
|---|--:|--:|--:|--:|
| forge-identity | 1 | 0 | 0 | 1 |

## By demo

| Demo | Findings |
|---|--:|
| 01-taskflow | 1 |
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
an optional local HS256 JWT (plaintext `JWT_SIGNING_KEY` until 51.04) and local
`admin`/`member` roles in product Postgres.

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
