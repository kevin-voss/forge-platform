-- Scheduler placements (epic 08 / step 08.01).
-- Unique (deployment_id, replica_index) makes placement idempotent per replica.

CREATE TABLE control.placements (
    id             TEXT PRIMARY KEY,
    deployment_id  UUID NOT NULL REFERENCES control.deployments (id) ON DELETE CASCADE,
    replica_index  INTEGER NOT NULL,
    node_id        TEXT NOT NULL,
    strategy       TEXT NOT NULL,
    reason         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT placements_node_id_not_blank CHECK (length(btrim(node_id)) > 0),
    CONSTRAINT placements_strategy_not_blank CHECK (length(btrim(strategy)) > 0),
    CONSTRAINT placements_replica_index_nonneg CHECK (replica_index >= 0),
    CONSTRAINT placements_deployment_replica_unique UNIQUE (deployment_id, replica_index)
);

CREATE INDEX idx_placements_deployment
    ON control.placements (deployment_id);
