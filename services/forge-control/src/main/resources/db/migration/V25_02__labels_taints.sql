-- Epic 25 / step 25.02: node labels, taints, architecture/OS + placement selectors.

ALTER TABLE control.nodes
    ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS taints_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS architecture TEXT NOT NULL DEFAULT 'amd64',
    ADD COLUMN IF NOT EXISTS os TEXT NOT NULL DEFAULT 'linux';

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS node_selector_json JSONB,
    ADD COLUMN IF NOT EXISTS tolerations_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS platform_json JSONB;
