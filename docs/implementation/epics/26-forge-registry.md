# Epic 26: Forge Registry

## Status

Planning

## Milestone

**M2 — Production platform.** Forge Registry is the entry point for M2's production posture: every later capability (canary rollouts, database images, plugin images) assumes images are addressed by digest and served by a Forge-owned, project-scoped registry rather than the plain anonymous registry epic 06 pushes to today.

## Goal

Stand up Forge Registry — a Go service on port `4113` — as the platform's own OCI-compliant, project-scoped image registry: authenticated push/pull, immutable version tags, digest resolution, image-signing metadata, garbage collection, retention policies, per-project storage quotas, a vulnerability-scan integration hook, mirror/pull-through support, and region replication. When this epic is done, `forge-build` pushes signed images to `registry.forge.internal/<organization>/<project>/<service>:<version>`, every deployment revision records the resolved digest, and Runtime pulls by digest so a deployment is byte-for-byte reproducible regardless of what a mutable tag points to later. Proven by `demos/26-registry`.

## Why this epic exists

Epic 06 proved source-to-deployment against a plain, anonymous Docker Distribution registry on `:5000` — fine for a single-tenant demo, unsafe for a platform with multiple organizations and projects. Production deployments need authenticated push/pull, tags that cannot be silently overwritten, a garbage-collection story so storage does not grow unbounded, and a digest recorded on the deployment record so "what's running" is never ambiguous. Forge Registry is the platform's own answer to that, built to run identically on a laptop and on a fleet of VMs.

## Relationship to shipped epics

Extends **epic 06 — Forge Build**. Epic 00 provisions a plain Docker Distribution container on `:5000` purely as blob storage; epic 06's `06.04` tags and pushes directly to it. Forge Registry becomes the authenticated, project-scoped façade in front of that same blob-storage container: `06.04`'s push call is redirected (additive config, not a rewrite) at `registry.forge.internal`, and the plain `:5000` registry keeps working unchanged as the local/dev storage backend underneath Forge Registry. Build's OpenAPI contract gains one additive field — `imageDigest` on the build result — it does not change shape. No existing Build, Control, or Runtime endpoint is broken.

## Primary code areas

* `services/forge-registry/` — new Go service: repository model, auth, tag/digest resolution, GC, replication
* `services/forge-build/` — push target updated to the Forge Registry endpoint (additive config, extends `06.04`)
* `services/forge-control/` — deployment revision gains a recorded `imageDigest` (extends `06.06`)
* `contracts/openapi/forge-registry.openapi.yaml` — repository/tag/digest/audit API surface
* `demos/26-registry/`

## Suggested language

Go. Matches Build and Gateway, and the OCI distribution ecosystem (`distribution/distribution`, containerd libraries) is Go-native, minimizing custom protocol work for the registry HTTP API v2.

## Spec references

* `docs/architecture/standalone-cloud.md` § Forge Registry
* `specs.md` → Step 06: Forge Build (registry push/pull integration, `06.04` tag + push)
* `specs.md` → Step 00 (local OCI registry foundation)
* [`epics/06-forge-build.md`](06-forge-build.md) → `06.04`, `06.06`

## Dependencies

* [`06-forge-build`](06-forge-build.md) — image producer this epic authenticates and fronts
* [`02-forge-control`](02-forge-control.md) — deployment revision record that carries the digest
* `09-forge-identity` — service-account authentication for push/pull
* `20-declarative-resource-api` — `Repository`/`Image` resource conventions this epic's API follows

## Out of scope for this epic

* Multi-arch manifest construction (Build's concern; Registry only stores and serves what Build pushes)
* Full Notary/cosign signature verification enforcement (this epic captures signing *metadata*; enforcing "reject unsigned images" is a `Policy` rule, epic 33)
* Acting as a general-purpose public registry (Docker Hub replacement) — project-scoped, platform-internal use only
* Building a vulnerability scanner (this epic wires an integration *hook*; the scanner itself is external)

## Portability contract

A product manifest must never contain a registry hostname, IP, credential, or provider registry ARN (no ECR repository URI, no ACR login server). Products reference images only as `registry.forge.internal/<organization>/<project>/<service>:<version>`; Forge Registry resolves that name identically everywhere.

* **Docker / bare metal**: single Forge Registry container fronting the epic-00 distribution blob store on a local volume.
* **Hetzner / AWS / Azure**: same Forge Registry image; blob storage backed by an attached disk (epic 30 Forge Volumes) or a distributed bucket (epic 31); an external managed registry (ECR, ACR) may be configured only as an optional **mirror/pull-through adapter**, never as a requirement.

## Success demo

```bash
make demo DEMO=26
```

```text
forge-build pushes invoice-api:1.4.0, signed → Forge Registry
  → repository forge-labs/invoice-platform/invoice-api created (project-scoped)
  → tag 1.4.0 is immutable: a second push of "1.4.0" is rejected; digest sha256:… recorded
  → deployment revision stores the digest, not the tag
  → Runtime pulls registry.forge.internal/forge-labs/invoice-platform/invoice-api@sha256:… — reproducible regardless of what "1.4.0" points to later
  → retention policy garbage-collects untagged digests older than 30 days
  → registry audit log shows every push/pull with actor + timestamp
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 26.01 | Registry skeleton + project-scoped repositories | Go service on 4113; repository model keyed by org/project/service |
| 26.02 | Auth via Identity service accounts + push/pull ACLs | Reject unauthenticated/unauthorized push and pull |
| 26.03 | Immutable tags + digest recording + Build integration | Reject tag overwrite; wire `06.04` push target |
| 26.04 | Image signing metadata + vulnerability-scan hook | Record signer/signature; call out to an external scanner |
| 26.05 | Garbage collection + retention policies + storage quotas | Reclaim untagged digests; enforce per-project byte quota |
| 26.06 | Mirror / pull-through + region replication | Optional upstream mirror; async cross-region digest copy |
| 26.07 | Registry audit log | Every push/pull/delete recorded with actor and result |
| 26.08 | Demo `26-registry` + gate | End-to-end push → sign → immutable tag → digest deploy |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* Forge Registry implements the OCI Distribution HTTP API v2 so standard Docker/containerd clients work against it unmodified.
* The plain distribution container from epic 00 remains the default local blob-storage backend; Forge Registry adds the auth/metadata/quota/GC layer in front of it rather than reimplementing blob storage.
* Service-account tokens (epic 09) are the only push/pull credential; no per-user passwords are stored by Registry itself.
* Immutability applies to explicit version tags; a `latest`-style moving tag remains mutable and is documented as non-reproducible.
* Signing metadata capture (this epic) and signature enforcement (Policy, epic 33) are deliberately split so Registry stays a storage/serving concern.

## Open questions

* Should digest recording happen in Build (`06.04`) or be re-resolved by Control at deploy time? **Assumption:** Build records the digest returned by Registry's push response; Control trusts and stores it verbatim, avoiding a second resolution round-trip.
* Is garbage collection online (safe while pulls are in flight) or does it require a maintenance window? **Assumption:** online GC using a mark-and-sweep pass that only reclaims digests unreferenced by any tag or deployment revision for longer than a grace period.
* How is region replication triggered — every push, or on demand per region? **Assumption:** async, on-demand per region attached to a `NodePool`/`Region` (epic 39 groundwork), not a synchronous fan-out on every push.
* Does the vulnerability-scan hook block push, or run asynchronously after push? **Assumption:** asynchronous post-push scan that annotates the image record; blocking on scan results is a `Policy` rule (`requireSignedImages`-style), not Registry's own gate.

## Next step to implement

**26.01 — Registry skeleton + project-scoped repositories** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `26.01-registry-skeleton-and-repositories.md` and assign its `N`).
