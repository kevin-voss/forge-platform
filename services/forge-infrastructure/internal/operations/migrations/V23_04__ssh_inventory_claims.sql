CREATE TABLE IF NOT EXISTS infrastructure.ssh_inventory_claims (
  provider_name   TEXT NOT NULL,
  address         TEXT NOT NULL,
  claimed_by_pool TEXT,
  claimed_at      TIMESTAMPTZ,
  PRIMARY KEY (provider_name, address)
);
CREATE INDEX IF NOT EXISTS ssh_inventory_claims_unclaimed_idx
  ON infrastructure.ssh_inventory_claims (provider_name)
  WHERE claimed_by_pool IS NULL;
