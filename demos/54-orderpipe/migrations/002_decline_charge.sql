-- OrderPipe 54.05 — injectable charge-failure toggle for saga compensation proofs.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS decline_charge BOOLEAN NOT NULL DEFAULT FALSE;
