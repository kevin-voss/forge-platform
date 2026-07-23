-- Managed PostgreSQL resource model (epic 18 / step 18.01).
-- Product DB metadata only — never stores Control's own connection credentials.

CREATE TABLE control.db_instance (
    id                   UUID PRIMARY KEY,
    project_id           UUID NOT NULL REFERENCES control.projects (id) ON DELETE RESTRICT,
    name                 TEXT NOT NULL,
    status               TEXT NOT NULL,
    engine               TEXT NOT NULL DEFAULT 'postgres',
    deletion_protection  BOOLEAN NOT NULL DEFAULT true,
    status_reason        TEXT,
    endpoint_ref         TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_instance_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT db_instance_engine_not_blank CHECK (length(btrim(engine)) > 0),
    CONSTRAINT db_instance_status_valid CHECK (
        status IN ('provisioning', 'available', 'error', 'deleting')
    ),
    CONSTRAINT db_instance_project_name_unique UNIQUE (project_id, name)
);

CREATE INDEX idx_db_instance_project_id ON control.db_instance (project_id);
CREATE INDEX idx_db_instance_status ON control.db_instance (status);

CREATE TABLE control.db_database (
    id          UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES control.db_instance (id) ON DELETE RESTRICT,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_database_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT db_database_instance_name_unique UNIQUE (instance_id, name)
);

CREATE INDEX idx_db_database_instance_id ON control.db_database (instance_id);

CREATE TABLE control.db_credential (
    id          UUID PRIMARY KEY,
    database_id UUID NOT NULL REFERENCES control.db_database (id) ON DELETE RESTRICT,
    username    TEXT NOT NULL,
    secret_ref  TEXT,
    status      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_credential_username_not_blank CHECK (length(btrim(username)) > 0),
    CONSTRAINT db_credential_status_valid CHECK (
        status IN ('pending', 'active', 'rotating', 'revoked')
    )
);

CREATE INDEX idx_db_credential_database_id ON control.db_credential (database_id);

CREATE TABLE control.db_attachment (
    id              UUID PRIMARY KEY,
    database_id     UUID NOT NULL REFERENCES control.db_database (id) ON DELETE RESTRICT,
    application_id  UUID NOT NULL REFERENCES control.applications (id) ON DELETE RESTRICT,
    env_var         TEXT NOT NULL DEFAULT 'DATABASE_URL',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_attachment_env_var_not_blank CHECK (length(btrim(env_var)) > 0),
    CONSTRAINT db_attachment_database_app_unique UNIQUE (database_id, application_id)
);

CREATE INDEX idx_db_attachment_database_id ON control.db_attachment (database_id);
CREATE INDEX idx_db_attachment_application_id ON control.db_attachment (application_id);

CREATE TABLE control.db_backup (
    id          UUID PRIMARY KEY,
    database_id UUID NOT NULL REFERENCES control.db_database (id) ON DELETE RESTRICT,
    location    TEXT,
    status      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_backup_status_valid CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed')
    )
);

CREATE INDEX idx_db_backup_database_id ON control.db_backup (database_id);
