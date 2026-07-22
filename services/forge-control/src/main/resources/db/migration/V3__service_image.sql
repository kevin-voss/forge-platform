-- Record the latest built image reference on a Control service (epic 06 / step 06.06).

ALTER TABLE control.services
    ADD COLUMN image TEXT,
    ADD COLUMN image_digest TEXT,
    ADD COLUMN image_commit TEXT,
    ADD COLUMN image_build_id TEXT;

ALTER TABLE control.services
    ADD CONSTRAINT services_image_not_blank
        CHECK (image IS NULL OR length(btrim(image)) > 0);
