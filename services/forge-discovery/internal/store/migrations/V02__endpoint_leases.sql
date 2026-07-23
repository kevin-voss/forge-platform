ALTER TABLE discovery.endpoints
  ADD COLUMN IF NOT EXISTS ready          BOOLEAN     NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS lease_seconds  INT         NOT NULL DEFAULT 20,
  ADD COLUMN IF NOT EXISTS expires_at     TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '20 seconds'),
  ADD COLUMN IF NOT EXISTS unready_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_endpoints_expiry ON discovery.endpoints (expires_at) WHERE phase <> 'Unready';
CREATE INDEX IF NOT EXISTS idx_endpoints_node_phase ON discovery.endpoints (node_id, phase);
