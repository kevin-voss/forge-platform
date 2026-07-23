-- Generic declarative resource store (epic 20 / step 20.01).
-- Single table for every registered kind; resource_version is a shared sequence
-- so watch cursors are meaningful across collections.

CREATE SEQUENCE control.resource_version_seq;

CREATE TABLE control.resources (
    id                 TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,
    api_version        TEXT NOT NULL DEFAULT 'forge.dev/v1',
    organization       TEXT NOT NULL,
    project            TEXT,
    environment        TEXT,
    name               TEXT NOT NULL,
    generation         BIGINT NOT NULL DEFAULT 1,
    resource_version   BIGINT NOT NULL,
    labels             JSONB NOT NULL DEFAULT '{}'::jsonb,
    annotations        JSONB NOT NULL DEFAULT '{}'::jsonb,
    spec               JSONB NOT NULL DEFAULT '{}'::jsonb,
    status             JSONB NOT NULL DEFAULT '{}'::jsonb,
    owner_refs         JSONB NOT NULL DEFAULT '[]'::jsonb,
    finalizers         JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT resources_kind_not_blank CHECK (length(btrim(kind)) > 0),
    CONSTRAINT resources_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT resources_org_not_blank CHECK (length(btrim(organization)) > 0),
    CONSTRAINT resources_env_requires_project CHECK (environment IS NULL OR project IS NOT NULL)
);

-- Scope-aware uniqueness: NULL project/environment must not defeat the check
-- (plain UNIQUE treats NULL as distinct in Postgres), so each scope gets its
-- own partial index. Soft-deleted rows (deleted_at set) free the name.
CREATE UNIQUE INDEX resources_scope_unique_env ON control.resources
    (kind, organization, project, environment, name)
    WHERE deleted_at IS NULL AND environment IS NOT NULL;

CREATE UNIQUE INDEX resources_scope_unique_project ON control.resources
    (kind, organization, project, name)
    WHERE deleted_at IS NULL AND project IS NOT NULL AND environment IS NULL;

CREATE UNIQUE INDEX resources_scope_unique_cluster ON control.resources
    (kind, organization, name)
    WHERE deleted_at IS NULL AND project IS NULL;

CREATE INDEX resources_kind_scope_idx ON control.resources (kind, organization, project, environment);
