-- Multi-node fleet inventory (epic 08 / step 08.02).
-- Runtime agents register with capacity; heartbeats refresh allocation + liveness.

CREATE TABLE control.nodes (
    id                  TEXT PRIMARY KEY,
    address             TEXT NOT NULL,
    capacity_json       JSONB NOT NULL,
    allocation_json     JSONB NOT NULL DEFAULT '{}'::jsonb,
    status              TEXT NOT NULL DEFAULT 'online',
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT nodes_id_not_blank CHECK (length(btrim(id)) > 0),
    CONSTRAINT nodes_address_not_blank CHECK (length(btrim(address)) > 0),
    CONSTRAINT nodes_status_valid CHECK (status IN ('online', 'offline', 'draining'))
);

CREATE INDEX idx_nodes_status ON control.nodes (status);
CREATE INDEX idx_nodes_last_heartbeat_at ON control.nodes (last_heartbeat_at);
