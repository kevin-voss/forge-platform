-- Epic 25 / step 25.04: priority classes, preemption audit, disruption budgets.

CREATE TABLE IF NOT EXISTS control.priority_classes (
    name              TEXT PRIMARY KEY,
    value             INT  NOT NULL,
    preemption_policy TEXT NOT NULL DEFAULT 'Never',
    description       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT priority_classes_policy_chk
        CHECK (preemption_policy IN ('Never', 'PreemptLowerPriority'))
);

INSERT INTO control.priority_classes(name, value, preemption_policy, description)
VALUES ('default', 0, 'Never', 'Implicit class for placements created before epic 25')
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS control.disruption_budgets (
    deployment_id   UUID PRIMARY KEY REFERENCES control.deployments(id) ON DELETE CASCADE,
    min_available   INT,
    max_unavailable INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT disruption_budgets_bounds_chk CHECK (
        (min_available IS NOT NULL AND min_available >= 0) OR
        (max_unavailable IS NOT NULL AND max_unavailable >= 0)
    ),
    CONSTRAINT disruption_budgets_one_mode_chk CHECK (
        (min_available IS NOT NULL AND max_unavailable IS NULL) OR
        (min_available IS NULL AND max_unavailable IS NOT NULL)
    )
);

ALTER TABLE control.placements
    ADD COLUMN IF NOT EXISTS priority_class TEXT NOT NULL DEFAULT 'default'
        REFERENCES control.priority_classes(name),
    ADD COLUMN IF NOT EXISTS preempted_by_placement_id TEXT;

CREATE TABLE IF NOT EXISTS control.preemption_events (
    id                     TEXT PRIMARY KEY,
    victim_placement_id    TEXT NOT NULL,
    preemptor_placement_id TEXT NOT NULL,
    victim_priority        INT  NOT NULL,
    preemptor_priority     INT  NOT NULL,
    node_id                TEXT NOT NULL,
    reason                 TEXT NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS preemption_events_preemptor_idx
    ON control.preemption_events (preemptor_placement_id);

CREATE INDEX IF NOT EXISTS preemption_events_victim_idx
    ON control.preemption_events (victim_placement_id);

CREATE INDEX IF NOT EXISTS preemption_events_created_idx
    ON control.preemption_events (created_at DESC);
