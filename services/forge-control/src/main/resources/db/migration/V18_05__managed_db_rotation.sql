-- Credential rotation timestamps + per-database deletion protection (18.05).

ALTER TABLE control.db_credential
    ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

ALTER TABLE control.db_database
    ADD COLUMN IF NOT EXISTS deletion_protection BOOLEAN NOT NULL DEFAULT true;
