-- Epic 25 / step 25.05: GPU capacity, TTL reservations, stateful placement.

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS stateful_json JSONB,
    ADD COLUMN IF NOT EXISTS gpu_json JSONB;

CREATE TABLE IF NOT EXISTS control.reservations (
    name                      TEXT PRIMARY KEY,
    resources_json            JSONB NOT NULL,
    expires_at                TIMESTAMPTZ NOT NULL,
    owner_ref                 TEXT,
    node_id                   TEXT,
    status                    TEXT NOT NULL DEFAULT 'active',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    consumed_by_placement_id  TEXT,
    CONSTRAINT reservations_status_chk
        CHECK (status IN ('active', 'consumed', 'expired'))
);

CREATE INDEX IF NOT EXISTS reservations_active_expires_idx
    ON control.reservations (expires_at)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS control.volume_locality (
    volume_ref  TEXT PRIMARY KEY,
    node_id     TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS control.migration_approvals (
    deployment_id  UUID NOT NULL,
    replica_index  INT  NOT NULL,
    approved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_by    TEXT,
    PRIMARY KEY (deployment_id, replica_index)
);
