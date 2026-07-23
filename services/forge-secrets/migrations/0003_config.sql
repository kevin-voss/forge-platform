-- Non-secret configuration values (epic 10 / step 10.03).
-- Plaintext by definition — do NOT store secrets here.

CREATE TABLE IF NOT EXISTS config_values (
    project_id  TEXT NOT NULL,
    environment TEXT NOT NULL,
    name        TEXT NOT NULL,
    value       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, environment, name)
);

CREATE INDEX IF NOT EXISTS idx_config_values_scope
    ON config_values (project_id, environment);
