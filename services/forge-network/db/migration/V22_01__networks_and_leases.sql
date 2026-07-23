CREATE SCHEMA IF NOT EXISTS network;

CREATE TABLE network.networks (
  id                 TEXT PRIMARY KEY,
  name               TEXT NOT NULL UNIQUE,
  cluster_cidr       CIDR NOT NULL,
  node_prefix_length INT  NOT NULL DEFAULT 24,
  ipv6_cidr          CIDR,
  generation         INT  NOT NULL DEFAULT 1,
  resource_version   BIGINT NOT NULL DEFAULT 1,
  phase              TEXT NOT NULL DEFAULT 'Ready',
  condition_reason   TEXT,
  condition_message  TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE network.node_leases (
  network_id  TEXT NOT NULL REFERENCES network.networks(id),
  node_id     TEXT NOT NULL,
  cidr        CIDR NOT NULL,
  gateway     INET NOT NULL,
  leased_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  released_at TIMESTAMPTZ,
  PRIMARY KEY (network_id, node_id)
);
CREATE UNIQUE INDEX node_leases_active_cidr
  ON network.node_leases (network_id, cidr) WHERE released_at IS NULL;

CREATE TABLE network.workload_leases (
  network_id  TEXT NOT NULL REFERENCES network.networks(id),
  node_id     TEXT NOT NULL,
  workload_id TEXT NOT NULL,
  address     INET NOT NULL,
  leased_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  released_at TIMESTAMPTZ,
  PRIMARY KEY (network_id, workload_id)
);
CREATE UNIQUE INDEX workload_leases_active_address
  ON network.workload_leases (network_id, address) WHERE released_at IS NULL;
