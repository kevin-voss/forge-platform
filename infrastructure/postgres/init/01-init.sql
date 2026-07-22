-- Forge Platform local bootstrap schema placeholder.
-- Platform services add their own migrations in later steps.

CREATE SCHEMA IF NOT EXISTS forge;

CREATE TABLE IF NOT EXISTS forge.schema_migrations (
    id TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO forge.schema_migrations (id)
VALUES ('00-foundation')
ON CONFLICT (id) DO NOTHING;
