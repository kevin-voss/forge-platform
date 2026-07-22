# Epic 43: Plugins and extensions

## Status

Planning

## Milestone

**M3 — Global platform.** Sixth and final M3 epic. This epic owns the **M3 exit capstone demo**, `demos/43-full-standalone-platform`.

## Goal

Turn every provider-specific or policy-specific seam built across M1–M3 into a stable, versioned extension point — infrastructure providers, DNS providers, notification providers, model runtimes, identity providers, storage backends, build strategies, policy rules, agent tools, workflow steps, and metrics adapters — loaded as signed, permission-scoped, isolated plugins. When this epic is done, an `InfrastructureProvider` (and the other extension kinds) can carry a `spec.plugin` pointing at an OCI image with a declared permission manifest, and the platform's own portability promise is enforced by code rather than convention. This epic also owns the **M3 exit capstone**: `demos/43-full-standalone-platform` deploys a real polyglot product, provisions its dependencies, injects a bad release, and proves the full detect → diagnose → approve → rollback loop — multi-node, autoscaled, and provider-portable — on local Docker and, optionally, an unmodified cloud target.

## Why this epic exists

Every prior epic hardcoded a finite set of built-in adapters (local disks, the bundled DNS zone, a fixed set of identity flows). A platform that must run identically across Docker, bare metal, Hetzner, AWS, and Azure forever needs those adapters to be swappable and extensible without forking the platform — and needs a capstone that proves the *whole* accumulated platform (not just one epic) still delivers on its founding promise once every seam is pluggable.

## Relationship to shipped epics

Extends **epic 23 Forge Infrastructure**'s `InfrastructureProvider` kind (built-in adapters only through M1/M2) into a plugin-loaded provider model; extends **epic 34 DNS and certificates** (DNS providers), **epic 09 Forge Identity** (identity providers), **epic 31 distributed object storage** (storage backends), **epic 06 Forge Build** (build strategies), **epic 33 Forge Policy** (policy rules), **epic 15 Forge Agents** (agent tools), **epic 16 Forge Workflows** (workflow steps), and **epic 12 Forge Observe** (metrics adapters) with the same pattern. Compatibility rule: every extension point is a new optional `spec.plugin` field or resource variant on an already-shipped kind — an install with zero plugins registered behaves exactly as the epic that introduced the built-in adapter (an `InfrastructureProvider` with no `spec.plugin` works exactly as epic 23 shipped it).

## Primary code areas

* `services/forge-control/` — plugin registry, permission-manifest enforcement, plugin lifecycle/health tracking
* `plugins/` — reference plugin implementations for each extension point (infrastructure, DNS, notification, model runtime, identity, storage, build, policy, agent tool, workflow step, metrics adapter)
* `demos/43-full-standalone-platform/` — the M3 exit capstone: product, infra, and the full operations loop

## Suggested language

Plugin host logic lives in each owning service's existing language (Kotlin for Control-hosted registry, Go for infrastructure/DNS/storage services); plugins themselves are OCI images invoked over a stable, language-agnostic gRPC/HTTP extension protocol.

## Spec references

* `docs/architecture/standalone-cloud.md` § Plugins and extensions
* `docs/architecture/standalone-cloud.md` § Full standalone platform capstone
* `specs.md` → Step 19 (the M1-scope capstone precedent this epic's capstone extends, not replaces)

## Dependencies

Effectively the whole M1–M3 catalog, since the capstone exercises the accumulated platform; named explicitly:

* Epic `23-forge-infrastructure` (catalogued, not yet materialized) — `InfrastructureProvider` gaining plugin loading
* Epic `34-dns-and-certificates` (catalogued, not yet materialized) — DNS/certificate provider plugins
* Epic [`09-forge-identity`](09-forge-identity.md) — identity provider plugins
* Epic `31-distributed-object-storage` (catalogued, not yet materialized) — storage backend plugins
* Epic [`06-forge-build`](06-forge-build.md) — build strategy plugins
* Epic `33-forge-policy` (catalogued, not yet materialized) — policy rule plugins
* Epic [`15-forge-agents`](15-forge-agents.md), [`16-forge-workflows`](16-forge-workflows.md) — agent tool and workflow step plugins
* Epic [`37-alerts-and-incidents`](37-alerts-and-incidents.md) — the incident-driven rollback the capstone demonstrates
* Epic `24-forge-autoscaler` (catalogued, not yet materialized) — the autoscaling the capstone demonstrates

## Out of scope for this epic

* A public plugin marketplace or discovery UI (epic 40's Console may list installed plugins, but a marketplace is out of scope)
* Untrusted third-party plugin execution without signing — signing and permission manifests are required from this epic's first step, never a later hardening pass
* Re-implementing epic 19's capstone product — this epic's capstone is a distinct product and scenario (see below)

## Portability contract

This is the epic where portability is *proven*, not just declared. `demos/43-full-standalone-platform` must run unmodified on local Docker (the default gate) and, opt-in via `FORGE_DEMO_TARGET=hetzner|aws|azure`, against a real cloud target using the identical manifests — because every provider-specific behavior is now behind the plugin extension point rather than hardcoded anywhere in a platform service.

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata:
  name: hetzner-primary
spec:
  plugin:
    image: registry.forge.internal/forge/plugins/infra-hetzner:2.1.0
    permissions: [nodes.create, nodes.delete, disks.create]
status:
  phase: Ready
  health: healthy
```

**How this capstone differs from epic 19's** (`demos/09-full-platform`): epic 19 proved the single-node AI-native operations loop — detect → diagnose → approve → rollback — on one Runtime node, with a fixed, built-in set of platform services, and a demo script invoking Workflows directly. This epic's capstone proves the *same loop again*, but now:

* **multi-node** — the scheduler (epic 08/25) spreads replicas across two or more nodes;
* **autoscaled** — the autoscaler (epic 24) scales API and worker replicas under generated traffic, not a fixed replica count;
* **provider-portable** — the `InfrastructureProvider` plugin point means the manifest names no provider, so the identical manifest runs on Docker or an opt-in cloud target;
* **incident-driven** — the rollback is triggered through epic 37's Alerts/Incident pipeline, not a demo script calling Workflows directly.

It does not re-implement epic 19's product; it extends the operational proof to the full M1–M3 surface.

## Success demo

```bash
make demo DEMO=43
```

```text
Deploy a React frontend + Spring Boot API via forge.yaml (no provider names)
Provision PostgreSQL (29), a durable queue (28), and object storage (31)
Configure authentication (09); enable autoscaling (24)
Generate traffic → API and worker replicas scale up
Terminate a runtime node → workloads reschedule (08) without downtime
Inject a bad release
→ error rate breaches an AlertRule (37) → Incident created
→ investigation agent (15) diagnoses the recent revision
→ workflow (16) requests rollback approval
→ operator approves → deployment rolls back (07/27) → healthy revision restored
Optional: repeat the same manifests with FORGE_DEMO_TARGET=hetzner|aws|azure
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 43.01 | Plugin extension API + permission manifest + signing | Stable protocol; declared permissions; mandatory signature verification |
| 43.02 | Infrastructure provider plugin loading | `InfrastructureProvider` gains `spec.plugin`; built-ins remain default |
| 43.03 | DNS/notification/identity/storage provider plugins | Same plugin pattern applied to four more extension points |
| 43.04 | Build/policy/agent-tool/workflow-step/metrics-adapter plugins | Remaining six extension points |
| 43.05 | Plugin lifecycle, health reporting, isolated execution | Start/stop/upgrade a plugin; sandboxed execution; health surfaced to Console |
| 43.06 | Capstone product scaffold on plugin-portable manifests | React + Spring Boot product; zero provider-named fields |
| 43.07 | Capstone operations loop: multi-node, autoscaled, incident-driven | Node loss + autoscale + Alerts-triggered rollback, end to end |
| 43.08 | Demo `43-full-standalone-platform` + M3 exit capstone gate | Full scenario on local Docker; optional cloud-target run |

Steps are catalogued but not yet materialized. Materialize them with
[`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
values are assigned at that point.

## Assumptions

* Plugins are OCI images invoked over a stable extension protocol rather than dynamically loaded code in-process, so a misbehaving plugin cannot crash its host service.
* Signing is mandatory for every plugin from day one; there is no "unsigned plugin, dev mode only" bypass, since this is the epic that closes the platform's trust boundary at its most extensible point.
* The capstone's cloud-target run is genuinely optional and never gates CI; `FORGE_DEMO_TARGET` unset always means local Docker.
* The capstone product is new and minimal (React + Spring Boot), distinct from epic 19's five-service polyglot incident-management product — reusing epic 19's product would blur the "not a duplicate" requirement.

## Open questions

* Plugin isolation mechanism — separate container/process per plugin invocation, or a long-lived sidecar? Assumption: long-lived sidecar process per registered plugin (matches "lifecycle management" and "health reporting" requirements) rather than a cold-start-per-call model.
* Does a plugin permission manifest use the same `Policy` (epic 33) rule language, or its own schema? Assumption: its own minimal capability-list schema (`nodes.create`, `disks.create`, etc.), since plugin permissions describe what the plugin may ask the host to do, not what a user resource may contain.
* Version-compatibility matrix format — semver ranges per extension-point API version? Assumption: yes, each extension point publishes its own semver'd protocol version; a plugin declares the range it supports, checked at registration.

## Next step to implement

Materialize steps via `PLAN_STEPS.md`, then implement **43.01 — Plugin extension API + permission manifest + signing** first: every extension point (43.02–43.04) and the capstone itself depend on the extension protocol and trust model existing first.
