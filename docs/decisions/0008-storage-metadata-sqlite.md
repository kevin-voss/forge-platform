# 0008. Storage metadata index uses embedded SQLite

## Status

Accepted (epic 13 / step 13.02)

## Context

Forge Storage needs a durable, project-scoped index of buckets and object
metadata beside content-addressed blobs on the local filesystem. Options were
embedded SQLite, a small append-only journal, or the platform Postgres.

## Decision

Use **embedded SQLite** at `$FORGE_STORAGE_ROOT/meta/index.db` (override with
`FORGE_STORAGE_META_PATH`):

1. The storage service stays self-contained — no hard dependency on platform
   Postgres for object listing/isolation.
2. WAL mode is enabled for crash-friendly durability on the named Compose volume.
3. Every query is scoped by `project_id`; uniqueness is `(project_id, name)` for
   buckets and `(project_id, bucket_id, key)` for objects.

## Consequences

* Restart durability of metadata follows the same volume as blob data
* Later steps (streaming, checksums, quotas) extend the same schema via migrations
* If a platform-wide metadata store becomes standard, a migration path can move
  the index without changing the HTTP contract
