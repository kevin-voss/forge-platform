-- 22.03: WireGuard peer registry + per-network peer_version.

ALTER TABLE network.networks
  ADD COLUMN IF NOT EXISTS peer_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS network.wireguard_peers (
  network_id           TEXT NOT NULL REFERENCES network.networks(id) ON DELETE CASCADE,
  node_id              TEXT NOT NULL,
  public_key           TEXT NOT NULL,
  endpoint             TEXT,
  status               TEXT NOT NULL DEFAULT 'active',
  rotates_to           TEXT,
  retire_old_after     TIMESTAMPTZ,
  peer_set_version     BIGINT NOT NULL DEFAULT 0,
  applied_peer_version BIGINT NOT NULL DEFAULT 0,
  online               BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (network_id, node_id),
  CONSTRAINT wireguard_peers_status_chk
    CHECK (status IN ('active', 'rotating', 'retiring'))
);

CREATE INDEX IF NOT EXISTS wireguard_peers_network_online
  ON network.wireguard_peers (network_id) WHERE online = TRUE;
