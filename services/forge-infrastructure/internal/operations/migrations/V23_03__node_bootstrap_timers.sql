CREATE TABLE IF NOT EXISTS infrastructure.node_bootstrap_timers (
  node_id          TEXT PRIMARY KEY,
  phase            TEXT NOT NULL,
  started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deadline_at      TIMESTAMPTZ NOT NULL,
  drain_started_at TIMESTAMPTZ,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS node_bootstrap_timers_deadline_idx
  ON infrastructure.node_bootstrap_timers (deadline_at);
