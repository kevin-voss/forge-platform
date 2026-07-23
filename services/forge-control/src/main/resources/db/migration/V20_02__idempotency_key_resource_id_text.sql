-- Resource ids are ULID-prefixed TEXT (e.g. wgt_01J…), not UUIDs.
ALTER TABLE control.idempotency_keys
    ALTER COLUMN resource_id TYPE TEXT USING resource_id::text;
