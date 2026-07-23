-- Epic 25 / step 25.01: real CPU/memory/disk requests + limits and allocatable capacity.

ALTER TABLE control.nodes
    ADD COLUMN IF NOT EXISTS allocatable_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS reserved_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS requests_json JSONB,
    ADD COLUMN IF NOT EXISTS limits_json JSONB,
    ADD COLUMN IF NOT EXISTS trace_json JSONB;
