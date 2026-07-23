-- Per-project quotas + incremental usage counters (13.06).
-- `blobs` already exists from 0003; this migration adds quota/usage only.
CREATE TABLE IF NOT EXISTS project_quota (
  project_id TEXT PRIMARY KEY,
  quota_bytes INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS project_usage (
  project_id TEXT PRIMARY KEY,
  used_bytes INTEGER NOT NULL DEFAULT 0,
  object_count INTEGER NOT NULL DEFAULT 0
);
