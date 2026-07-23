-- ScalingPolicy resource + idempotency + durable watch events (epic 24 / step 24.01).

CREATE SEQUENCE IF NOT EXISTS scaling_policy_rv_seq;

CREATE TABLE IF NOT EXISTS scaling_policies (
  id                TEXT PRIMARY KEY,
  name              TEXT NOT NULL,
  project           TEXT NOT NULL,
  environment       TEXT NOT NULL,
  generation        INT NOT NULL DEFAULT 1,
  resource_version  BIGINT NOT NULL DEFAULT nextval('scaling_policy_rv_seq'),
  spec_json         JSONB NOT NULL,
  status_json       JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project, environment, name)
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  key               TEXT PRIMARY KEY,
  body_hash         TEXT NOT NULL,
  response_status   INT NOT NULL,
  response_body     JSONB NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS scaling_policy_events (
  resource_version  BIGINT PRIMARY KEY,
  event_type        TEXT NOT NULL CHECK (event_type IN ('ADDED', 'MODIFIED', 'STATUS_MODIFIED', 'DELETED')),
  project           TEXT NOT NULL,
  environment       TEXT NOT NULL,
  name              TEXT NOT NULL,
  payload           JSONB NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scaling_policy_events_created_at_idx
  ON scaling_policy_events (created_at);
