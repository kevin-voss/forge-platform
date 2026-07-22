-- Anti-affinity + pending queue (epic 08 / step 08.04).
-- Pending placements have no node yet; capacity is reserved only on place.

ALTER TABLE control.placements
    ALTER COLUMN node_id DROP NOT NULL;

ALTER TABLE control.placements
    ADD COLUMN status TEXT NOT NULL DEFAULT 'placed',
    ADD COLUMN anti_affinity TEXT NOT NULL DEFAULT 'soft',
    ADD COLUMN slots INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN service_id TEXT;

ALTER TABLE control.placements
    ADD CONSTRAINT placements_status_check
        CHECK (status IN ('placed', 'pending')),
    ADD CONSTRAINT placements_anti_affinity_check
        CHECK (anti_affinity IN ('soft', 'hard')),
    ADD CONSTRAINT placements_slots_positive
        CHECK (slots >= 1),
    ADD CONSTRAINT placements_pending_node_null
        CHECK (
            (status = 'placed' AND node_id IS NOT NULL AND length(btrim(node_id)) > 0)
            OR (status = 'pending' AND node_id IS NULL)
        );

CREATE INDEX idx_placements_pending
    ON control.placements (created_at)
    WHERE status = 'pending';

CREATE INDEX idx_placements_service_placed
    ON control.placements (service_id)
    WHERE status = 'placed' AND service_id IS NOT NULL;
