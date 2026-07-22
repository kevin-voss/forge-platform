-- Deployment history + restart safety (epic 07 / step 07.05).
-- Append-only transition trail for controller-driven status changes.

CREATE TABLE control.deployment_events (
    id                BIGSERIAL PRIMARY KEY,
    deployment_id     UUID NOT NULL REFERENCES control.deployments (id) ON DELETE CASCADE,
    at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    from_status       TEXT NOT NULL,
    to_status         TEXT NOT NULL,
    image             TEXT,
    desired_replicas  INTEGER,
    actual_replicas   INTEGER,
    reason            TEXT,
    CONSTRAINT deployment_events_from_status_not_blank CHECK (length(btrim(from_status)) > 0),
    CONSTRAINT deployment_events_to_status_not_blank CHECK (length(btrim(to_status)) > 0)
);

CREATE INDEX idx_deployment_events_dpl_at
    ON control.deployment_events (deployment_id, at);
