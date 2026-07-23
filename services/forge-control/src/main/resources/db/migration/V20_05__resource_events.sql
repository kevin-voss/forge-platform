-- Durable resource change log for watch/replay (epic 20 / step 20.05).
-- resource_version matches control.resources.resource_version (shared sequence).

CREATE TABLE control.resource_events (
    resource_version BIGINT PRIMARY KEY,
    event_id         TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    kind             TEXT NOT NULL,
    organization     TEXT NOT NULL,
    project          TEXT,
    environment      TEXT,
    resource_id      TEXT NOT NULL,
    resource_name    TEXT NOT NULL,
    generation       BIGINT NOT NULL,
    payload          JSONB NOT NULL,
    actor            TEXT,
    request_id       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT resource_events_type_known CHECK (
        event_type IN ('ADDED', 'MODIFIED', 'STATUS_MODIFIED', 'DELETED')
    ),
    CONSTRAINT resource_events_kind_not_blank CHECK (length(btrim(kind)) > 0),
    CONSTRAINT resource_events_org_not_blank CHECK (length(btrim(organization)) > 0),
    CONSTRAINT resource_events_name_not_blank CHECK (length(btrim(resource_name)) > 0)
);

CREATE INDEX resource_events_kind_scope_idx
    ON control.resource_events (kind, organization, project, environment, resource_version);

CREATE INDEX resource_events_created_at_idx
    ON control.resource_events (created_at);
