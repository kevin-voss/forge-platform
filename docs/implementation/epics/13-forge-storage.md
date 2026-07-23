# Epic 13: Forge Storage

## Status

In progress

## Goal

Provide a project-scoped object storage service that platform components and deployed products use for build artifacts, deployment artifacts, user uploads, database backups, model files, agent outputs, and workflow outputs. When this epic is done, `services/forge-storage` (Rust, host port `4107`) exposes bucket and object APIs with streamed upload/download, SHA-256 integrity, byte-range requests, signed access tokens with expiry, per-project quotas, hard delete, and a local filesystem backend that survives restart — all validated by `demos/13-object-storage`.

## Why this epic exists

Multiple later epics need durable blob storage that respects project isolation: Build stores images/artifacts, managed Postgres stores backups, Models stores model files, Agents/Workflows store run outputs, and Memory stores source documents. Centralizing storage behind one contract (rather than each service writing to ad-hoc volumes) gives consistent isolation, integrity, quotas, and signed access across the platform.

## Primary code areas

* `services/forge-storage/` — Rust service (Axum recommended), local FS backend
* `contracts/openapi/forge-storage.openapi.yaml` — HTTP contract
* `demos/13-object-storage/` — Compose demo + acceptance script
* `docs/architecture/` — storage architecture note + ADR for backend/signing

## Suggested language

Rust (per `specs.md` §4 and Step 13). Framework at implementer discretion (Axum recommended); streaming via `tokio`/`hyper` body streaming.

## Spec references

* `specs.md` → Step 13: Forge Storage (features, uses, demo, tests, acceptance)
* `specs.md` → §2.2 runtime boundary, §5.4 logging (structured logs)
* `specs.md` → Step 09: Forge Identity (project scope used for isolation)

## Dependencies

* Epic [`00-repository-foundation`](00-repository-foundation.md) complete (Compose, Make, ports, docs tree)
* Epic [`01-runtime-contract`](01-runtime-contract.md) planned conventions (health endpoints, structured logs, `PORT`, graceful shutdown)
* Epic [`09-forge-identity`](09-forge-identity.md) for project-scope context (minimum: project identifier + auth token verification from `09.05`/`09.06`). A documented `FORGE_AUTH_MODE=dev` bypass is permitted until Identity is enforced, consistent with MASTER_PLAN open question 2.

## Out of scope for this epic

* Content deduplication as a hard requirement (design a dedup-friendly SHA-256 content-addressed layout; full dedup optional)
* S3-compatible API surface / external object stores (local FS backend only)
* Multi-node replication or erasure coding
* Client SDKs under `packages/*`
* Consumers wiring (Build/Models/Memory integration lands in their epics)

## Success demo

```bash
make demo DEMO=13
```

`demos/13-object-storage` performs the Step 13 flow against a running `forge-storage`:

```text
1. create bucket
2. upload large object (streamed)
3. download object (streamed)
4. verify SHA-256 checksum
5. request a byte range
6. reject an expired signed token
7. delete object
8. restart service → object still present (durability)
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [13.01](../steps/13-forge-storage/13.01-skeleton-local-fs-backend.md) | Skeleton + local FS backend | Complete | Rust service, health, Compose, port 4107, FS layout |
| [13.02](../steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) | Buckets + metadata + project isolation | Not started | Depends on 13.01; bucket/object metadata store, project scope |
| [13.03](../steps/13-forge-storage/13.03-streamed-upload-download.md) | Streamed upload/download | Not started | Depends on 13.02; no full in-memory large files |
| [13.04](../steps/13-forge-storage/13.04-sha256-range-requests.md) | SHA-256 + range requests | Not started | Depends on 13.03; integrity + `Range` support |
| [13.05](../steps/13-forge-storage/13.05-signed-tokens-expiry.md) | Signed tokens + expiry | Not started | Depends on 13.04; HMAC signed access + expiry enforcement |
| [13.06](../steps/13-forge-storage/13.06-quotas-delete-durability.md) | Quotas + delete + restart durability | Not started | Depends on 13.05; per-project quota, hard delete, restart safety |
| [13.07](../steps/13-forge-storage/13.07-demo-and-gate.md) | Demo `13-object-storage` + gate | Not started | Depends on 13.06; epic acceptance gate |

## Assumptions

* Service lives at `services/forge-storage/`, host port `4107` (per MASTER_PLAN port map).
* Local FS backend rooted at a container volume path `FORGE_STORAGE_ROOT` (default `/data/storage`); durable across restart via a named Compose volume.
* Objects are stored content-addressed by SHA-256 (`objects/<aa>/<full-hash>`) with a separate metadata index keyed by `(project, bucket, key)`; this layout enables optional dedup without requiring it.
* Signed tokens are HMAC-SHA256 over `(method, project, bucket, key, expiry)` with a service secret `FORGE_STORAGE_SIGNING_KEY`; no external KMS.
* Project isolation derives from an authenticated project id (Identity) or `X-Forge-Project` header in `dev` auth mode.
* Metadata index persistence uses embedded storage (SQLite or an append-only journal) under `FORGE_STORAGE_ROOT`; exact choice fixed in `13.02` ADR.

## Open questions

* Metadata store: embedded SQLite vs a small append-only journal vs the platform Postgres. Assumption: embedded SQLite under the storage volume to keep the service self-contained; revisit if platform standard emerges.
* Is content dedup in-scope for acceptance, or documented-only? Assumption: content-addressed layout now, dedup as a documented optional optimization.
* Should quotas be per-project only, or also per-bucket? Assumption: per-project byte quota for this epic; per-bucket optional.
* Signed-token algorithm/format: bearer query param vs `Authorization` header. Assumption: query param `?token=` for download links plus header support.

## Next step to implement

**[13.02](../steps/13-forge-storage/13.02-buckets-metadata-project-isolation.md) — Buckets + metadata + project isolation** (depends on 13.01).
