-- Access audit trail for secrets/config (epic 10 / step 10.06).
-- Never stores secret values — metadata only.

CREATE TABLE IF NOT EXISTS audit_events (
    id           BIGSERIAL PRIMARY KEY,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    project_id   TEXT NOT NULL,
    environment  TEXT,
    action       TEXT NOT NULL,
    principal    TEXT NOT NULL,
    name         TEXT,
    version      INT,
    result       TEXT NOT NULL,
    source       TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_scope
    ON audit_events (project_id, at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_name
    ON audit_events (project_id, name, at DESC)
    WHERE name IS NOT NULL;
