-- Epic 25 / step 25.03: topology facts + affinity/topology-spread persistence.

ALTER TABLE control.nodes
    ADD COLUMN IF NOT EXISTS zone TEXT NOT NULL DEFAULT 'default',
    ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT 'default',
    ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'docker';

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS affinity_json JSONB,
    ADD COLUMN IF NOT EXISTS topology_spread_json JSONB;
