-- Service bindings: which secrets/config a service consumes (epic 10 / step 10.04).

CREATE TABLE IF NOT EXISTS service_bindings (
    project_id   TEXT NOT NULL,
    environment  TEXT NOT NULL,
    service      TEXT NOT NULL,
    secret_names TEXT[] NOT NULL DEFAULT '{}',
    config_names TEXT[] NOT NULL DEFAULT '{}',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, environment, service)
);

CREATE INDEX IF NOT EXISTS idx_service_bindings_scope
    ON service_bindings (project_id, environment);
