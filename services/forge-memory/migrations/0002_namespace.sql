-- forge-memory namespaces (17.04)
-- Uniqueness becomes (project_id, namespace, collection name); records inherit scope.

CREATE TABLE collections_new (
  project_id TEXT NOT NULL,
  namespace TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  dim INTEGER NOT NULL,
  distance TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY (project_id, namespace, name)
);

INSERT INTO collections_new (project_id, namespace, name, dim, distance, count, created_at)
SELECT project_id, '', name, dim, distance, count, created_at FROM collections;

CREATE TABLE records_new (
  project_id TEXT NOT NULL,
  namespace TEXT NOT NULL DEFAULT '',
  collection TEXT NOT NULL,
  id TEXT NOT NULL,
  offset INTEGER NOT NULL,
  metadata TEXT,
  document_ref TEXT,
  deleted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, namespace, collection, id),
  FOREIGN KEY (project_id, namespace, collection)
    REFERENCES collections_new(project_id, namespace, name) ON DELETE CASCADE
);

INSERT INTO records_new (project_id, namespace, collection, id, offset, metadata, document_ref, deleted)
SELECT c.project_id, '', r.collection, r.id, r.offset, r.metadata, r.document_ref, r.deleted
FROM records r
JOIN collections c ON c.name = r.collection;

DROP TABLE records;
DROP TABLE collections;
ALTER TABLE collections_new RENAME TO collections;
ALTER TABLE records_new RENAME TO records;

CREATE INDEX IF NOT EXISTS idx_collections_project_ns
  ON collections(project_id, namespace);
CREATE INDEX IF NOT EXISTS idx_records_scope
  ON records(project_id, namespace, collection, deleted, offset);
