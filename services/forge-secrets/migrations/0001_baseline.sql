-- Baseline schema for forge-secrets (epic 10 / step 10.01).
-- Per-project data keys are stored wrapped by the master key; plaintext never persists.

CREATE TABLE IF NOT EXISTS project_data_keys (
    project_id     TEXT PRIMARY KEY,
    wrapped_key    BYTEA NOT NULL,
    key_version    INT  NOT NULL DEFAULT 1,
    master_key_id  TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_project_data_keys_master_key_id
    ON project_data_keys (master_key_id);
