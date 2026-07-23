CREATE TABLE IF NOT EXISTS infrastructure.provider_operations (
  id             TEXT PRIMARY KEY,
  provider_name  TEXT NOT NULL,
  kind           TEXT NOT NULL,
  target_kind    TEXT NOT NULL,
  target_id      TEXT,
  natural_key    TEXT NOT NULL,
  request        JSONB NOT NULL,
  status         TEXT NOT NULL DEFAULT 'pending',
  result         JSONB,
  error          TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at   TIMESTAMPTZ,
  UNIQUE (provider_name, natural_key)
);
CREATE INDEX IF NOT EXISTS provider_operations_status_idx
  ON infrastructure.provider_operations (status);
