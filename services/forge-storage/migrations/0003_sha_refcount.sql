-- Content-addressed blob refcounts for SHA-256 integrity / optional dedup (13.04).
-- Safe hard-delete of shared blobs lands in 13.06.
CREATE TABLE IF NOT EXISTS blobs (
  sha256 TEXT PRIMARY KEY,
  size_bytes INTEGER NOT NULL,
  refcount INTEGER NOT NULL DEFAULT 0
);
