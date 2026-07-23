-- Step 20.07: name column for Deployment resources (unique per environment).
-- Backfill from owning service name; edge case of colliding service names across
-- applications in one environment is deferred (single-app-per-project demos).

ALTER TABLE control.deployments
    ADD COLUMN IF NOT EXISTS name TEXT;

UPDATE control.deployments d
SET name = s.name
FROM control.services s
WHERE d.service_id = s.id
  AND (d.name IS NULL OR btrim(d.name) = '');

ALTER TABLE control.deployments
    ALTER COLUMN name SET NOT NULL;

ALTER TABLE control.deployments
    DROP CONSTRAINT IF EXISTS deployments_name_not_blank;

ALTER TABLE control.deployments
    ADD CONSTRAINT deployments_name_not_blank CHECK (length(btrim(name)) > 0);

CREATE UNIQUE INDEX IF NOT EXISTS deployments_environment_name_unique
    ON control.deployments (environment_id, name);
