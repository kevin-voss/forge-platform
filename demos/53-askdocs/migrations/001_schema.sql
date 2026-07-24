-- AskDocs schema (epic 53.01+). Idempotent — safe to re-run on boot.
-- documents/chunks back storage ingest (53.02); messages back chat history.
-- status stays ingesting until embeddings mark ready (53.03).

CREATE TABLE IF NOT EXISTS documents (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    object_key TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'ingesting'
               CHECK (status IN ('ingesting', 'ready')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS documents_status_idx ON documents (status);
CREATE INDEX IF NOT EXISTS documents_created_at_idx ON documents (created_at);

CREATE TABLE IF NOT EXISTS chunks (
    id          TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    ordinal     INTEGER NOT NULL DEFAULT 0,
    text        TEXT NOT NULL DEFAULT '',
    memory_id   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS chunks_document_id_idx ON chunks (document_id);

CREATE TABLE IF NOT EXISTS messages (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    text       TEXT NOT NULL,
    citations  JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS messages_session_id_idx ON messages (session_id);
CREATE INDEX IF NOT EXISTS messages_created_at_idx ON messages (created_at);
