-- Activates terminating-but-visible deletion (epic 20 / step 20.06).
-- deletion_timestamp marks "delete requested, finalizers pending";
-- deleted_at remains the terminal soft-delete marker.

ALTER TABLE control.resources
    ADD COLUMN IF NOT EXISTS deletion_timestamp TIMESTAMPTZ;
