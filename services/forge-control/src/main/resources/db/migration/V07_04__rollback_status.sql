-- Unhealthy rollout → automatic rollback (epic 07 / step 07.04).
-- deployments.status already exists (02.02); expand usage at the app layer.
-- Snapshot last-known-good image/replicas on the service for safe restore after in-place image updates.

ALTER TABLE control.services
    ADD COLUMN last_healthy_deployment_id UUID REFERENCES control.deployments (id) ON DELETE SET NULL,
    ADD COLUMN last_healthy_image TEXT,
    ADD COLUMN last_healthy_replicas INTEGER;

ALTER TABLE control.services
    ADD CONSTRAINT services_last_healthy_replicas_nonneg
        CHECK (last_healthy_replicas IS NULL OR last_healthy_replicas >= 0);

ALTER TABLE control.reconcile_status
    ADD COLUMN deployment_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN last_healthy_image TEXT,
    ADD COLUMN rollout_started_at TIMESTAMPTZ;
