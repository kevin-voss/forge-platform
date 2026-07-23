-- forge-storage metadata index (13.02)
CREATE TABLE IF NOT EXISTS buckets (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(project_id, name)
);

CREATE TABLE IF NOT EXISTS objects (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  bucket_id TEXT NOT NULL REFERENCES buckets(id),
  key TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT,
  content_type TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(project_id, bucket_id, key)
);

CREATE INDEX IF NOT EXISTS idx_objects_project ON objects(project_id, bucket_id);
