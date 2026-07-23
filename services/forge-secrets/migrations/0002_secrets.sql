-- Encrypted secret versions (epic 10 / step 10.02).
-- Ciphertext + nonce only; plaintext is never persisted.

CREATE TABLE IF NOT EXISTS secrets (
    id               BIGSERIAL PRIMARY KEY,
    project_id       TEXT NOT NULL,
    environment      TEXT NOT NULL,
    name             TEXT NOT NULL,
    version          INT  NOT NULL,
    ciphertext       BYTEA NOT NULL,
    nonce            BYTEA NOT NULL,
    data_key_version INT  NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, environment, name, version)
);

CREATE INDEX IF NOT EXISTS idx_secrets_scope
    ON secrets (project_id, environment, name);
