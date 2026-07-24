-- SnapNote schema (epic 52.01). Idempotent — safe to re-run on boot.
-- attachments is a stub for 52.02+ (object storage / worker); notes CRUD is live.

CREATE TABLE IF NOT EXISTS notes (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS notes_created_at_idx ON notes (created_at);

CREATE TABLE IF NOT EXISTS attachments (
    id            TEXT PRIMARY KEY,
    note_id       TEXT NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
    object_key    TEXT NOT NULL,
    content_type  TEXT NOT NULL DEFAULT 'application/octet-stream',
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'ready', 'failed')),
    thumbnail_key TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS attachments_note_id_idx ON attachments (note_id);
CREATE INDEX IF NOT EXISTS attachments_status_idx ON attachments (status);
