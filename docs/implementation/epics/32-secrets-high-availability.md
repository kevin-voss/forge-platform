# Epic 32: Secrets high availability

## Status

Planning

## Milestone

**M2 — Production platform.** HA secrets — key rotation without downtime, short-lived delivery, full audit trails — is a named M2 production requirement; epic 10's single master key and static secrets are a development baseline, not a production posture.

## Goal

Extend Forge Secrets with production-grade key management and delivery: envelope encryption, master-key rotation that re-wraps every per-organization data key transactionally, secret versioning, service-identity authentication, short-lived encrypted secret delivery, full audit trails, access policies, automatic rotation workflows (including database credential rotation tied to epic 29), certificate-secret support, a guarantee that no plaintext ever persists in Control, and secret memory clearing where the runtime allows it. Proven by `demos/32-secrets-ha`: rotate the master key with zero downtime and no redeploys, then rotate an application secret and watch a redeploy pick up the new value while the audit log records both actions with actor and timestamp.

## Why this epic exists

Epic 10 shipped a single master key wrapping per-project data keys — enough to prove encrypted storage, project isolation, and runtime injection, but a single master key with no rotation path is a production liability: it can never be rotated without a service-wide re-encryption event, and there is no workflow for the recurring operational need to rotate database credentials or certificates. This epic makes secrets rotation a routine, zero-downtime operation instead of a one-time bootstrap decision.

## Relationship to shipped epics

Extends **epic 10 — Forge Secrets**. `10.01`–`10.02`'s master-key-plus-data-key model becomes the default envelope-encryption implementation this epic operationalizes, not a replacement; master-key rotation and version metadata are additive fields on the existing secret-version record (`10.02`). The `10.04` runtime-injection contract — env vars delivered at deploy time — is preserved exactly in shape; short-lived encrypted delivery is layered underneath as an additive negotiation between Runtime and Secrets, not a new endpoint. `10.06`'s access-audit and log-masking model is extended with policy-based access control, not rewritten.

## Primary code areas

* `services/forge-secrets/` — extended (still port `4104`): key rotation workflow, versioning, short-lived delivery, audit/policy
* `services/forge-runtime/` — workload-identity authentication against Secrets (extends `04.03` delivery path)
* `demos/32-secrets-ha/`
* `contracts/openapi/forge-secrets.openapi.yaml` — additive rotation/versioning/policy fields

## Suggested language

Rust — continues epic 10 unchanged; the AES-256-GCM/XChaCha20-Poly1305 encryption foundation from `10.01`–`10.02` is reused directly for envelope encryption and key rotation.

## Spec references

* `docs/architecture/standalone-cloud.md` § Secrets high availability
* `specs.md` → Step 10: Forge Secrets and configuration
* [`epics/10-forge-secrets.md`](10-forge-secrets.md) → `10.01`–`10.06`

## Dependencies

* [`10-forge-secrets`](10-forge-secrets.md) — envelope-encryption baseline this epic extends
* `09-forge-identity` — service-identity authentication for delivery
* [`04-forge-runtime`](04-forge-runtime.md) — short-lived delivery consumer
* `29-database-high-availability` — target of automatic database-credential rotation
* `20-declarative-resource-api` — additive `Secret` version/rotation-policy conventions

## Out of scope for this epic

* Requiring an HSM or external KMS — the env-provided master key remains the default everywhere; HSM/KMS integration is an optional adapter through the `KeyProvider` seam `10`'s open questions already document
* Dynamic, lease-based secrets as a hard requirement (Vault-style short-lived database credentials) — rotation workflows cover the common production case; full leasing is future work
* Cross-organization secret sharing — isolation remains a hard requirement, not a configuration option

## Portability contract

A product manifest never contains a secret value or key material — it only references a secret by name (unchanged from epic 10). The `KeyProvider` defaults to the environment-provided master key on every target — local Docker, bare metal, Hetzner, AWS, Azure — with identical rotation behavior everywhere. An HSM- or cloud-KMS-backed `KeyProvider` may be configured only as an optional adapter behind the same interface; `demos/32-secrets-ha` must pass using the default env-provided key on local Docker with no external KMS dependency.

**Data-safety rules (non-negotiable):**

* No plaintext secret value ever persists in Control's resource store or appears in any resource `status` field — this was already true in epic 10 and remains an invariant this epic must not weaken while adding rotation metadata.
* Master-key rotation re-wraps every affected per-organization data key **transactionally** — an interrupted rotation leaves every secret decryptable under either the old or the new master key, never neither.
* Secret memory is cleared after use wherever the runtime/language allows it (zeroing buffers on drop).
* The audit trail is **append-only** and records actor, action, secret name, version, and timestamp for every read and write — audit records themselves are never deletable through the Secrets API.

## Success demo

```bash
make demo DEMO=32
```

```text
Secrets master key v1 wraps every organization's data keys
  → DATABASE_PASSWORD set as secret v1, FEATURE_X set as config
  → Runtime authenticates with its node certificate → Secrets verifies workload identity
    → short-lived encrypted payload returned → plaintext never appears in resource status
  → master key rotated to v2 → every org data key re-wrapped transactionally, zero downtime, no redeploy required
  → DATABASE_PASSWORD rotated to v2 → automatic database-credential rotation workflow updates the live role (epic 29)
  → redeploy picks up v2 → audit log shows both rotations with actor + timestamp
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 32.01 | Envelope encryption + per-organization data keys | Extends `10.01`–`10.02`; formalizes the wrap/unwrap chain |
| 32.02 | Master-key rotation workflow | Transactional re-wrap of all org data keys, zero downtime |
| 32.03 | Secret versioning + service-identity authentication | Extends `10.03`; version history, identity-bound access |
| 32.04 | Short-lived secret delivery | Extends `10.04`; time-boxed encrypted payload to Runtime |
| 32.05 | Automatic rotation workflows + database credential rotation | Scheduled rotation; ties into epic 29's live role update |
| 32.06 | Certificate secrets support | Store/rotate TLS key material as a secret type |
| 32.07 | Audit trails + access policies | Extends `10.06`; policy-based read/write authorization |
| 32.08 | Secret memory clearing + demo `32-secrets-ha` + gate | Zeroing on drop where practical; end-to-end rotation gate |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* The `KeyProvider` seam documented as an open question in epic 10 is where this epic's HSM/KMS optionality attaches; no new seam is invented.
* Master-key rotation is a platform-operator action (not per-project), consistent with one master key wrapping every organization's data keys.
* Service-identity authentication reuses epic 09's node/workload identity model rather than introducing a second identity system specific to Secrets.
* Database credential rotation (tied to epic 29) updates the live Postgres role via the `forge-data` provisioner and only then rotates the stored secret version, so there is never a window where the stored secret doesn't match the live credential.
* Certificate secrets are stored using the same encrypted-secret envelope as any other secret type; they are not a structurally different resource.

## Open questions

* Can a rotation be rolled back if a workload fails to redeploy with the new value? **Assumption:** yes — both the old and new master-key-wrapped versions of a data key remain valid for a documented grace period after rotation, and a secret rotation retains the previous version until the operator confirms the new version is in use.
* Does short-lived delivery mean a new token per request, or a token cached for a bounded TTL? **Assumption:** a bounded-TTL encrypted payload cached by Runtime for the TTL window, avoiding a round-trip to Secrets on every workload restart within that window.
* How is "automatic" database credential rotation scheduled — fixed interval, or triggered externally? **Assumption:** fixed interval per `Secret`'s `rotationPolicy.schedule`, with an on-demand manual trigger also available via the CLI.
* Does certificate-secret rotation coordinate with epic 34's certificate issuance, or are they independent? **Assumption:** independent in this epic — epic 34 owns issuance and expiry monitoring; epic 32 only provides encrypted storage and delivery for the resulting key material. Convergence is a documented follow-up once both epics ship.

## Next step to implement

**32.01 — Envelope encryption + per-organization data keys** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `32.01-envelope-encryption-and-data-keys.md` and assign its `N`).
