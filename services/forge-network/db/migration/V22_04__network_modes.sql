-- 22.04: per-node membership/colocation + computed per-pair transport cache.

CREATE TABLE IF NOT EXISTS network.nodes (
  node_id            TEXT PRIMARY KEY,
  network_membership TEXT,
  docker_colocated   BOOLEAN NOT NULL DEFAULT false,
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS network.network_routes (
  network_id  TEXT NOT NULL REFERENCES network.networks(id) ON DELETE CASCADE,
  from_node   TEXT NOT NULL,
  to_node     TEXT NOT NULL,
  transport   TEXT NOT NULL,
  computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (network_id, from_node, to_node),
  CONSTRAINT network_routes_transport_chk
    CHECK (transport IN ('docker', 'provider-private', 'wireguard'))
);

CREATE INDEX IF NOT EXISTS network_routes_transport_idx
  ON network.network_routes (network_id, transport);
