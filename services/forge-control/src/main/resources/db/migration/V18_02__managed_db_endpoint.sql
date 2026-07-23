-- Managed DB instance endpoint metadata (epic 18 / step 18.02).
-- Product container host/port/container_id — never Control's own JDBC URL.

ALTER TABLE control.db_instance
    ADD COLUMN host TEXT,
    ADD COLUMN port INTEGER,
    ADD COLUMN container_id TEXT;

ALTER TABLE control.db_instance
    ADD CONSTRAINT db_instance_port_range CHECK (
        port IS NULL OR (port >= 1 AND port <= 65535)
    );

ALTER TABLE control.db_database
    ADD COLUMN status TEXT NOT NULL DEFAULT 'provisioning',
    ADD COLUMN status_reason TEXT;

ALTER TABLE control.db_database
    ADD CONSTRAINT db_database_status_valid CHECK (
        status IN ('provisioning', 'available', 'error')
    );
