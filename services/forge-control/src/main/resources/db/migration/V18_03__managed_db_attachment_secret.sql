-- Managed DB attachment secret reference (epic 18 / step 18.03).
-- Connection URL lives only in Secrets; Control keeps secret_ref.

ALTER TABLE control.db_attachment
    ADD COLUMN secret_ref TEXT;
