-- Desired/actual reconcile model (epic 07 / step 07.01).
-- desired_replicas already exists on deployments (02.02); add rollout policy + snapshot table.

ALTER TABLE control.deployments
    ADD COLUMN rollout_batch_size INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN rollout_timeout_s INTEGER NOT NULL DEFAULT 120;

ALTER TABLE control.deployments
    ADD CONSTRAINT deployments_rollout_batch_size_pos CHECK (rollout_batch_size >= 1),
    ADD CONSTRAINT deployments_rollout_timeout_s_pos CHECK (rollout_timeout_s >= 1);

CREATE TABLE control.reconcile_status (
    deployment_id       UUID PRIMARY KEY REFERENCES control.deployments (id) ON DELETE CASCADE,
    last_run_at         TIMESTAMPTZ NOT NULL,
    desired_json        JSONB NOT NULL,
    actual_json         JSONB NOT NULL,
    plan_json           JSONB NOT NULL,
    controller_healthy  BOOLEAN NOT NULL DEFAULT TRUE
);
