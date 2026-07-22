-- Control plane domain schema (epic 02 / step 02.02).
-- Flyway also creates schema `control` when configured with schemas=control.

CREATE TABLE control.projects (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT projects_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT projects_slug_not_blank CHECK (length(btrim(slug)) > 0),
    CONSTRAINT projects_slug_unique UNIQUE (slug)
);

CREATE TABLE control.environments (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES control.projects (id) ON DELETE RESTRICT,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT environments_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT environments_project_name_unique UNIQUE (project_id, name)
);

CREATE TABLE control.applications (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES control.projects (id) ON DELETE RESTRICT,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT applications_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT applications_project_name_unique UNIQUE (project_id, name)
);

CREATE TABLE control.services (
    id              UUID PRIMARY KEY,
    application_id  UUID NOT NULL REFERENCES control.applications (id) ON DELETE RESTRICT,
    name            TEXT NOT NULL,
    port            INTEGER NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT services_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT services_port_range CHECK (port >= 1 AND port <= 65535),
    CONSTRAINT services_application_name_unique UNIQUE (application_id, name)
);

CREATE TABLE control.deployments (
    id                  UUID PRIMARY KEY,
    service_id          UUID NOT NULL REFERENCES control.services (id) ON DELETE RESTRICT,
    environment_id      UUID NOT NULL REFERENCES control.environments (id) ON DELETE RESTRICT,
    image               TEXT NOT NULL,
    desired_replicas    INTEGER NOT NULL DEFAULT 1,
    status              TEXT NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT deployments_image_not_blank CHECK (length(btrim(image)) > 0),
    CONSTRAINT deployments_desired_replicas_nonneg CHECK (desired_replicas >= 0),
    CONSTRAINT deployments_status_not_blank CHECK (length(btrim(status)) > 0)
);

CREATE TABLE control.audit_log (
    id           UUID PRIMARY KEY,
    entity_type  TEXT NOT NULL,
    entity_id    UUID NOT NULL,
    action       TEXT NOT NULL,
    actor        TEXT NOT NULL,
    at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detail       JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT audit_log_entity_type_not_blank CHECK (length(btrim(entity_type)) > 0),
    CONSTRAINT audit_log_action_not_blank CHECK (length(btrim(action)) > 0),
    CONSTRAINT audit_log_actor_not_blank CHECK (length(btrim(actor)) > 0)
);

CREATE INDEX audit_log_entity_idx ON control.audit_log (entity_type, entity_id);
CREATE INDEX audit_log_at_idx ON control.audit_log (at);
