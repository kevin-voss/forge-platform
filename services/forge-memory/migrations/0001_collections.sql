-- forge-memory collections + records (17.02)
CREATE TABLE IF NOT EXISTS collections (
  name TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  dim INTEGER NOT NULL,
  distance TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_collections_project ON collections(project_id);

CREATE TABLE IF NOT EXISTS records (
  collection TEXT NOT NULL,
  id TEXT NOT NULL,
  offset INTEGER NOT NULL,
  metadata TEXT,
  document_ref TEXT,
  deleted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (collection, id),
  FOREIGN KEY (collection) REFERENCES collections(name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_records_collection ON records(collection, deleted, offset);
