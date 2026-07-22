-- Reschedule on node offline (epic 08 / step 08.05).
-- Lost placements keep the original node_id for audit; active (placed|pending)
-- rows remain unique per (deployment_id, replica_index). Replacement placements
-- record rescheduled_from_node.

ALTER TABLE control.placements
    DROP CONSTRAINT IF EXISTS placements_status_check;

ALTER TABLE control.placements
    DROP CONSTRAINT IF EXISTS placements_pending_node_null;

ALTER TABLE control.placements
    DROP CONSTRAINT IF EXISTS placements_deployment_replica_unique;

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS rescheduled_from_node TEXT;

ALTER TABLE control.placements
    ADD CONSTRAINT placements_status_check
        CHECK (status IN ('placed', 'pending', 'lost'));

ALTER TABLE control.placements
    ADD CONSTRAINT placements_status_node_check
        CHECK (
            (status = 'placed' AND node_id IS NOT NULL AND length(btrim(node_id)) > 0)
            OR (status = 'pending' AND node_id IS NULL)
            OR (status = 'lost' AND node_id IS NOT NULL AND length(btrim(node_id)) > 0)
        );

CREATE UNIQUE INDEX IF NOT EXISTS placements_active_deployment_replica
    ON control.placements (deployment_id, replica_index)
    WHERE status IN ('placed', 'pending');

CREATE INDEX IF NOT EXISTS idx_placements_lost_node
    ON control.placements (node_id)
    WHERE status = 'lost';
