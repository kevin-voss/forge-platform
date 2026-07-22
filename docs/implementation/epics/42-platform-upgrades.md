# Epic 42: Platform upgrades

## Status

Planning

## Milestone

**M3 — Global platform.** Fifth of the six M3 epics (38–43); 43 is the M3 exit capstone.

## Goal

Make upgrading Forge itself a safe, plannable operation instead of an ad hoc redeploy. When this epic is done, `forge upgrade plan` checks Control API compatibility, Runtime protocol versions, pending database migrations, and deprecated API usage, then produces an explicit upgrade plan; `forge upgrade apply` proceeds standby-first with health validation between waves across the control plane and every Runtime node; and a downgrade policy documents what can and cannot be safely reversed. Proven by `demos/42-platform-upgrade`.

## Why this epic exists

Epic 35 gave Control the ability to run mixed-version replicas briefly during a leader failover; a production platform also needs a *deliberate*, operator-initiated upgrade of the whole platform — Control, Runtime, Gateway, and every other service — with preflight checks and a rollback story, not just resilience to an unplanned crash. Without this epic, upgrading Forge itself is exactly the kind of undocumented, risky operation this platform exists to eliminate for its users' own applications.

## Relationship to shipped epics

Extends **epic 35 control-plane high availability**'s rolling-upgrade primitive (35 established that Control tolerates mixed-version replicas during failover; this epic formalizes full platform-wide upgrade orchestration on top of that same tolerance) and **epic 20**'s resource envelope (`generation`/`resourceVersion` are already versioned; this epic adds schema migration on top of that same envelope, never a new one). Compatibility rule: strictly additive tooling (`forge upgrade`) plus a documented N/N−1 API compatibility window on every service — no existing endpoint may break within that window; a genuinely breaking change requires a new API version path (a facade), never in-place mutation of a shipped contract.

## Primary code areas

* `tools/forge-cli/` — `forge upgrade plan` / `forge upgrade apply` subcommands
* `services/forge-control/` — schema migration framework, feature gates, release-channel metadata
* Every platform service — protocol version negotiation for Runtime, rolling-upgrade participation
* `demos/42-platform-upgrade/` — plan → standby-first rollout → health-gated wave acceptance

## Suggested language

Go for the CLI (matching epic 03); Kotlin for Control's migration tooling (matching epic 02); each service's upgrade participation stays in its own existing language.

## Spec references

* `docs/architecture/standalone-cloud.md` § Platform upgrades
* `specs.md` → Step 02 (Control migrations baseline), Step 03 (Forge CLI), Step 04 (Runtime protocol)

## Dependencies

* Epic [`35-control-plane-high-availability`](35-control-plane-high-availability.md) — rolling control-plane upgrade tolerance this epic formalizes into a full orchestrated upgrade
* Epic `20-declarative-resource-api` (catalogued, not yet materialized) — `generation`/`resourceVersion` schema envelope migrations extend
* Epic [`03-forge-cli`](03-forge-cli.md) — CLI the `forge upgrade` subcommand extends
* Epic [`04-forge-runtime`](04-forge-runtime.md) — node protocol versioning target
* Epic [`02-forge-control`](02-forge-control.md) — migration framework baseline being generalized

## Out of scope for this epic

* Automatic/unattended upgrades without an explicit preflight-approved plan
* Cross-major-version data transformation logic beyond migrations already defined per resource kind
* Plugin compatibility versioning (epic 43 owns the plugin version-compatibility matrix specifically)

## Portability contract

An upgrade plan and its release channel are metadata only — no provider-specific upgrade step ever appears in a plan. `forge upgrade plan` / `forge upgrade apply` run identically against a Docker install, bare metal, Hetzner, AWS, or Azure target because they only talk to Forge's own public APIs and node agents, never a cloud provider's own update mechanism (no dependency on, e.g., an AWS Systems Manager patch job or an Azure VM extension).

```yaml
apiVersion: forge.dev/v1
kind: UpgradePlan
metadata:
  name: 2026-08-upgrade
spec:
  targetVersion: 1.6.0
  channel: stable
  waves: [control-standby, control-primary, runtime-nodes, gateway]
status:
  phase: Ready
  preflight:
    controlCompatible: true
    runtimeVersionsSupported: true
    pendingMigrations: 2
    deprecatedApisInUse: []
```

## Success demo

```bash
make demo DEMO=42
```

```text
forge upgrade plan
→ checks Control API compatibility, Runtime protocol versions,
  pending database migrations, deprecated API usage
→ produces an UpgradePlan with ordered waves
forge upgrade apply
→ standby Control replicas upgrade first (epic 35 tolerates this)
→ health validated before promoting the upgraded replica to leader
→ Runtime nodes upgrade one wave at a time; workloads stay available
→ Gateway upgrades last; deprecated-API usage is flagged, not broken
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 42.01 | API versioning + N/N−1 compatibility contract | Documented compatibility window every service commits to |
| 42.02 | Resource-schema migration framework | Versioned migrations for the epic 20 resource envelope |
| 42.03 | Runtime protocol version negotiation | Runtime nodes advertise/negotiate supported protocol versions |
| 42.04 | Rolling control-plane upgrade orchestration | Standby-first Control upgrade building on epic 35 |
| 42.05 | Rolling runtime upgrade orchestration | Wave-based node upgrade with health gates between waves |
| 42.06 | Feature gates + release channels | Gate new behavior; stable/beta channel metadata |
| 42.07 | `forge upgrade plan` preflight + downgrade policy | Compatibility/migration/deprecation checks; documented downgrade rules |
| 42.08 | Demo `42-platform-upgrade` + epic gate | Plan → standby-first apply → health-gated waves acceptance |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* Every service commits to serving both its current and immediately prior API version for the duration of a documented compatibility window, so a rolling upgrade never has a moment with zero compatible caller.
* Resource-schema migrations are forward-only in production; downgrade policy documents which versions can be safely reverted to (typically only the immediately prior release) rather than promising universal downgrade.
* Feature gates default to off for new behavior until a release channel promotes them, keeping `stable` channel installs conservative.
* The demo exercises a minor-version upgrade wave; a major-version upgrade with schema migrations is documented but not required for the gate.

## Open questions

* Does `forge upgrade apply` support partial rollback mid-wave if a health check fails? Assumption: yes — a failed wave halts the plan and the CLI reports which wave to roll back, reusing epic 07's rollback mechanics where applicable.
* Are release channels per-organization or platform-wide? Assumption: platform-wide (the whole install is on one channel); per-organization channels are not needed until multi-tenant SaaS operation, which is out of scope for this self-hosted platform.
* How are deprecated APIs surfaced to callers before removal? Assumption: a `Deprecation` response header plus a preflight check in `forge upgrade plan` that lists any deprecated API a client is still calling.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **42.01 — API versioning + N/N−1 compatibility contract** first: every other step (migrations, protocol negotiation, rolling upgrades) depends on a documented compatibility guarantee existing first.
