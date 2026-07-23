-- Interim physical path for streamed objects (13.03).
-- Content-addressed SHA-256 layout lands in 13.04; keep this column so keys stay stable.
ALTER TABLE objects ADD COLUMN storage_path TEXT NOT NULL DEFAULT '';
