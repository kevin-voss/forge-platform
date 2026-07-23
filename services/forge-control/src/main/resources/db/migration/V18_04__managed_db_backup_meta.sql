-- Backup metadata for on-demand dump/restore (epic 18 / step 18.04).

ALTER TABLE control.db_backup
    ADD COLUMN checksum TEXT,
    ADD COLUMN size_bytes BIGINT,
    ADD COLUMN completed_at TIMESTAMPTZ,
    ADD COLUMN status_reason TEXT,
    ADD COLUMN restore_status TEXT,
    ADD COLUMN restore_target_database_id UUID REFERENCES control.db_database (id) ON DELETE SET NULL,
    ADD COLUMN restore_completed_at TIMESTAMPTZ,
    ADD COLUMN restore_status_reason TEXT;

ALTER TABLE control.db_backup
    ADD CONSTRAINT db_backup_restore_status_valid CHECK (
        restore_status IS NULL OR restore_status IN ('running', 'succeeded', 'failed')
    );
