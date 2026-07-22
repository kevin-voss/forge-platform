CREATE TABLE control.idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     UUID NOT NULL,
    response_status INTEGER NOT NULL,
    response_body   JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idempotency_keys_key_not_blank CHECK (length(btrim(key)) > 0)
);

CREATE INDEX idempotency_keys_created_at_idx ON control.idempotency_keys (created_at);
