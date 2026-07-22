# Epic 33: Forge Policy

## Status

Planning

## Milestone

**M2 — Production platform.** Governance and admission control is a named M2 requirement: production platforms need a way to reject non-compliant resources before they are written, not just observe compliance after the fact.

## Goal

Stand up Forge Policy — centralized admission control, authorization, quotas, and governance evaluated inline with every resource write in Forge Control. A `Policy` resource declares `scope.environment` and a rule list drawn from a fixed catalog: `requireMinimumReplicas`, `denyPrivilegedContainers`, `requireResourceLimits`, `requireSignedImages`, `requireDeletionProtectionForDatabases`, plus organization/project quotas, image-source restrictions, network restrictions, cost limits, region restrictions, audit policy, and deployment-approval policy. When this epic is done, admission runs before any resource write in Control, a violation is rejected with a typed error the CLI renders directly (not a generic 500), and compliant resources are unaffected. Proven by `demos/33-policy-admission`.

## Why this epic exists

Every prior epic assumes the caller is well-behaved: nothing stops a privileged container, an unbounded replica count, or a database deleted without protection today, short of the constraints each individual service happens to enforce. A platform running multiple organizations' production workloads needs one centralized, declarative place to say "reject this before it's written" — Forge Policy is that admission layer, evaluated the same way regardless of which resource kind or which service originated the write.

## Relationship to shipped epics

New capability, additive to **epic 02 — Forge Control** and **epic 09 — Forge Identity**. Admission runs as a new pre-write hook inserted into Control's existing `POST`/`PUT`/`PATCH` resource handlers; it does not change the shape of any existing endpoint — a compliant request gets the same response it always did, and a violating request gets a new, additive `4xx PolicyViolation` error shape alongside Control's existing error envelope. Policy composes with, but does not replace, Identity's authentication/authorization checks (`09.04`): Identity answers "who is this and are they allowed to call this API," Policy answers "does this specific resource spec meet the organization's rules."

## Primary code areas

* `services/forge-policy/` — new module/service (port `4120`): `Policy` resource, rule evaluation engine
* `services/forge-control/` — admission hook wired into the resource write path (extends epic 02's handlers)
* `tools/forge-cli/` — typed `PolicyViolation` error rendering
* `demos/33-policy-admission/`
* `contracts/openapi/forge-policy.openapi.yaml`

## Suggested language

Kotlin, evaluated as a module invoked synchronously from Forge Control's write path — mirroring the extract-seam pattern epics 07 and 08 already establish for reconciliation and scheduling. This keeps admission in the same request context Control already has (no extra network hop before every write) with a documented seam to extract it to a standalone service on `4120` later.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Policy
* `specs.md` → Step 02: Forge Control (resource write APIs this epic's admission hook intercepts)
* `specs.md` → Step 09: Forge Identity (authorization this epic composes with, not replaces)
* [`epics/02-forge-control.md`](02-forge-control.md)
* [`epics/09-forge-identity.md`](09-forge-identity.md)

## Dependencies

* [`02-forge-control`](02-forge-control.md) — write-path hook point
* [`09-forge-identity`](09-forge-identity.md) — authentication/authorization context Policy composes with
* `20-declarative-resource-api` — `Policy` resource + admission-contract conventions

## Out of scope for this epic

* Continuous compliance scanning of already-created resources (this epic is admission-time only; continuous drift detection is future work)
* A full policy-as-code DSL or rule engine (OPA/Rego-style custom rules) — the rule catalog is a fixed, typed set for this epic
* A visual policy editor (console, epic 40)

## Portability contract

Policy rules operate purely on the resource envelope — `spec` and `metadata` — never on a provider-specific field, so `denyPrivilegedContainers` or `requireResourceLimits` evaluates identically whether the resource will ultimately run on local Docker, bare metal, Hetzner, AWS, or Azure. A product manifest never encodes a policy bypass flag; policy exceptions, when they exist, are a platform-operator-owned `Policy` resource field, never something a product manifest can set for itself.

## Success demo

```bash
make demo DEMO=33
```

```text
Policy prod-guardrails: scope.environment production
  rules: [requireMinimumReplicas: 2, denyPrivilegedContainers, requireResourceLimits,
          requireSignedImages, requireDeletionProtectionForDatabases]

forge apply -f risky.yaml (replicas: 1, privileged: true)
  → Control rejects before write → CLI prints a typed PolicyViolation error → resource never created

forge apply -f compliant.yaml
  → admission passes → resource created normally

forge database delete invoice-db  (deletion protection still enabled)
  → rejected by requireDeletionProtectionForDatabases
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 33.01 | `Policy` resource + scope model | Organization/project/environment scoping envelope |
| 33.02 | Admission validation hook in Control's write path | Reject non-compliant resources before persistence |
| 33.03 | Mutation policies | Defaulting/normalization applied before persist |
| 33.04 | Resource-limit + replica-count + privileged-container rules | `requireResourceLimits`, `requireMinimumReplicas`, `denyPrivilegedContainers` |
| 33.05 | Image-source + signed-image policies | `requireSignedImages`; restrict allowed registries |
| 33.06 | Organization/project quotas + cost + region-restriction rules | Enforce resource ceilings and allowed regions |
| 33.07 | Deployment-approval + audit policy | Gate rollouts behind approval; policy-decision audit log |
| 33.08 | Demo `33-policy-admission` + gate | Reject non-compliant apply; accept compliant apply; block unprotected delete |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* Every rule in the catalog evaluates against the resource envelope only (spec + metadata + generation), never against live infrastructure state, so evaluation is fast and side-effect-free.
* A `Policy` resource is cluster- or organization-scoped (per epic 20's cluster-scoped-kind convention) and matched to a resource write by `scope.environment` plus resource kind.
* Admission failures return a structured error (`reason`, `rule`, `message`) that the CLI renders as a human-readable rejection, not a stack trace or generic 500.
* Mutation policies (defaulting/normalization) run before validation policies within the same admission pass, so a mutated value can still be validated.
* Deployment-approval policy reuses the same approval-gate mechanism epic 27 introduces for release approval, rather than inventing a second approval workflow.

## Open questions

* Can a `Policy` violation ever be an advisory warning instead of a hard rejection? **Assumption:** no — this epic ships hard-rejection admission only; a warn-only mode is a documented future enhancement, not required for the gate demo.
* How are conflicting policies at different scopes (organization vs. project vs. environment) resolved? **Assumption:** most-specific-scope-wins, with organization-level policies acting as a floor that a project/environment policy may tighten but never loosen.
* Does Policy see the fully-resolved resource (after other admission mutations) or the raw client-submitted spec? **Assumption:** Policy evaluates the fully-resolved spec, after this epic's own mutation-policy pass and after any Control-side defaulting, so validation rules see what will actually be persisted.
* Is quota enforcement real-time (checked on every write) or reconciled periodically? **Assumption:** real-time at admission time, computed from a live count/sum maintained by Control, not a periodic batch reconciliation — consistent with "admission runs before any resource write."

## Next step to implement

**33.01 — `Policy` resource + scope model** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `33.01-policy-resource-and-scope-model.md` and assign its `N`).
