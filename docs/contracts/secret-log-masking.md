# Secret log masking convention

Platform services must never emit configured secret values in logs. Forge Secrets
implements the reference masking filter and publishes a reusable library other
services should adopt.

## Rules

1. **Prefer not logging secrets** — log names, versions, fingerprints, and
   actions; never request/response bodies that carry plaintext secrets.
2. **Safety-net masking** — before writing a log line, replace every known secret
   value with the placeholder (`***` by default via `FORGE_MASK_PLACEHOLDER`).
3. **In-memory known values only** — register values seen on set / `:access` /
   resolve in a process-local set. Do **not** create a durable plaintext store
   for masking.
4. **Fail toward redaction** — if masking fails (non-UTF8, residual match), drop
   the dynamic content rather than emit the original line.

## Rust library (forge-secrets)

```text
services/forge-secrets/src/masking/mod.rs
```

Key types:

* `KnownSecrets` — thread-safe in-memory registry
* `mask_text(text, known, placeholder)` — pure redaction helper
* `MaskingMakeWriter` — `tracing_subscriber` writer that redacts stdout lines
* `global_known_secrets()` — process-wide registry used by forge-secrets

Other Rust services can copy this module (or depend on the same pattern) and wire
`MaskingMakeWriter` into their `tracing_subscriber` setup.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `FORGE_LOG_MASKING_ENABLED` | `true` | Disable only for local debugging |
| `FORGE_MASK_PLACEHOLDER` | `***` | Replacement token |
| `FORGE_AUDIT_ENABLED` | `true` | Access audit persistence |
| `FORGE_AUDIT_STRICT` | `false` | When true, audit insert failure fails the op |

## Access audit (companion)

Every secret/config access is also recorded in `audit_events` (DB-local; Events
service forwarding is a later seam). Query:

* `GET /v1/projects/{pid}/audit?name=&action=&since=`
* `GET /v1/projects/{pid}/envs/{env}/audit?name=&action=&since=`

Audit records never include a `value` field. Denied attempts are stored with
`result=denied`.
