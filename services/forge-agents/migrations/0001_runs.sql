-- Agent run audit store (15.04).
CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  status TEXT NOT NULL,
  result TEXT,
  error TEXT,
  step_count INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  ended_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_runs_project_started
  ON runs (project_id, started_at DESC);

CREATE TABLE IF NOT EXISTS run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  idx INTEGER NOT NULL,
  type TEXT NOT NULL,
  tool TEXT,
  args TEXT,
  observation TEXT,
  decision TEXT,
  ts TEXT NOT NULL,
  UNIQUE (run_id, idx)
);

CREATE INDEX IF NOT EXISTS idx_run_steps_run_idx
  ON run_steps (run_id, idx);
