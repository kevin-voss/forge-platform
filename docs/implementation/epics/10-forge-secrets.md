# Epic 10: Forge Secrets

## Status

In progress

## Goal

Stand up Forge Secrets — a Rust service on port `4104` — that stores encrypted, project/environment-scoped secrets and non-secret configuration, versions its encryption keys, injects secrets into Runtime workloads at deploy time, records access audits, and masks secret values in logs. When this epic is done, plaintext secrets are never stored or returned by list APIs, unauthorized projects cannot read another project's secrets, a rotated secret redeploys the workload with the new value, and logs mask configured secret values. Proven by `demos/10-secrets`.

## Why this epic exists

Applications need database passwords, API keys, and feature flags without hardcoding them or exposing them in the control plane. `specs.md` Step 10 defines encrypted storage separate from non-secret config, project isolation, key versioning, rotation, runtime injection, and log masking. This service is a prerequisite for realistic deployments and for later epics (managed Postgres URL injection in 18, agent credentials in 15).

## Primary code areas

* `services/forge-secrets/` — new Rust service (encrypted store, key management, config vs secret APIs, audit)
* `services/forge-runtime/` — secret injection at deploy time (consumes epic 04 env injection `04.03`)
* `tools/forge-cli/` — `forge secret` / `forge config` commands (`10.05`)
* `demos/10-secrets/` — set/rotate/redeploy acceptance
* `contracts/openapi/` — secrets + config API surface

## Suggested language

Rust (per `specs.md` Step 10). Authenticated encryption (AES-256-GCM or XChaCha20-Poly1305) via a vetted crate; a master key from the environment/bootstrap wraps per-project data keys.

## Spec references

* `specs.md` → Step 10: Forge Secrets and configuration (project/env-scoped secrets, encrypted storage, key versioning, metadata, rotation, access audit, runtime delivery, masking, config vs secrets)
* `specs.md` → Step 09 (Identity project scope for isolation)
* `specs.md` → Step 04 (Runtime env injection `04.03`)
* `docs/implementation/MASTER_PLAN.md` → Epic 10 catalog + port `4104`

## Dependencies

* Epic [`09-forge-identity`](09-forge-identity.md) — project scope + authorization (`09.04`) so only authorized principals/projects access secrets
* Epic [`04-forge-runtime`](04-forge-runtime.md) — workload env injection at create (`04.03`)
* Foundation `00` — Postgres, Compose, OTEL

## Out of scope for this epic

* Hardware security module (HSM) / KMS integration (env-provided master key only; document the seam)
* Dynamic/lease-based secrets (e.g. short-lived DB creds) — static secrets + rotation only
* Secret sharing across projects (isolation is the point)
* Client-side envelope encryption in the CLI (server-side encryption)

## Success demo

```bash
make demo DEMO=10
```

```text
Set DATABASE_PASSWORD (secret) + FEATURE_X (config) for project/env
Deploy demo app → app reports "secret present: true" and never the value
Rotate DATABASE_PASSWORD → redeploy → app sees the new value
List secrets → metadata only (names, versions), never plaintext
Logs → configured secret values masked
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [10.01](../steps/10-forge-secrets/10.01-skeleton-and-encryption-key-bootstrap.md) | Skeleton + encryption key bootstrap | Complete | Rust service on 4104, master key + data keys |
| [10.02](../steps/10-forge-secrets/10.02-encrypted-store-key-versioning-metadata.md) | Encrypted store + key versioning + metadata APIs | Complete | AEAD store, versions, metadata + `:access` |
| [10.03](../steps/10-forge-secrets/10.03-config-vs-secrets-and-project-isolation.md) | Config vs secrets APIs; project isolation | Complete | Separate config; enforce project scope |
| [10.04](../steps/10-forge-secrets/10.04-runtime-injection-at-deploy.md) | Runtime injection at deploy | Complete | Bindings + resolve; Control inject; fingerprint redeploy |
| [10.05](../steps/10-forge-secrets/10.05-cli-secret-and-config.md) | CLI `forge secret` / `forge config` | Not started | set/list/rotate; config set/show |
| [10.06](../steps/10-forge-secrets/10.06-access-audit-and-log-masking.md) | Access audit + log masking | Not started | Audit reads/writes; mask values in logs |
| [10.07](../steps/10-forge-secrets/10.07-demo-10-secrets.md) | Demo `10-secrets` + gate | Not started | set/rotate/redeploy; epic gate |

## Assumptions

* Encryption uses AES-256-GCM (or XChaCha20-Poly1305) with a per-project **data key** wrapped by a **master key** provided via `FORGE_SECRETS_MASTER_KEY` (bootstrap). Rotating the master key re-wraps data keys; rotating a secret creates a new secret version.
* "Config" values are non-secret and stored in plaintext (still project/env scoped); "secrets" are encrypted. Both are injected as env vars into workloads.
* Runtime injection happens at deploy: Control/Reconciler asks Secrets for the resolved env bundle for a service+env and passes it to Runtime's env injection (`04.03`). Plaintext lives only in the workload's environment, never persisted by Control.
* Authorization is delegated to Identity (`09.04`): `secret.read`/`secret.write`/`config.write` actions checked per project; project isolation is enforced server-side regardless of caller claims.
* Log masking is applied at the Secrets service and documented as a platform convention other services follow; a registry of secret value hashes/known values enables masking without storing plaintext elsewhere.

## Open questions

* Master key source: env var now vs KMS later. Assumption: env-provided master key with a documented `KeyProvider` seam for KMS/HSM later.
* Should config live in Secrets or a separate Config service? Assumption: same service, separate API + storage path (`specs.md` groups "Secrets and configuration").
* Injection mechanism: env vars only, or also mounted files? Assumption: env vars for this epic (matches Runtime `04.03`); file mounts optional later.
* Masking scope: only Secrets' own logs, or platform-wide filter? Assumption: Secrets masks its own logs + publishes a masking convention/library other services adopt incrementally.

## Next step to implement

**[10.05](../steps/10-forge-secrets/10.05-cli-secret-and-config.md) — CLI `forge secret` / `forge config`**.
