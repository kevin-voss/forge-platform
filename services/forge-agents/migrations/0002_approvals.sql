-- Human approval gate for destructive tools (15.06).
CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  project_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  args TEXT NOT NULL,
  status TEXT NOT NULL,
  decided_by TEXT,
  reason TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  decided_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_approvals_project_created
  ON approvals (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_approvals_run_status
  ON approvals (run_id, status);

CREATE INDEX IF NOT EXISTS idx_approvals_pending_expires
  ON approvals (status, expires_at);

-- Resume fields so awaiting_approval runs survive service restart.
CREATE TABLE IF NOT EXISTS run_resume (
  run_id TEXT PRIMARY KEY REFERENCES runs(id),
  input TEXT NOT NULL DEFAULT '',
  context TEXT NOT NULL DEFAULT '{}'
);
