CREATE SCHEMA IF NOT EXISTS discovery;

CREATE TABLE discovery.services (
  id               TEXT PRIMARY KEY,
  project          TEXT NOT NULL,
  environment      TEXT NOT NULL,
  name             TEXT NOT NULL,
  ports            JSONB NOT NULL DEFAULT '[]',
  aliases          JSONB NOT NULL DEFAULT '[]',
  resource_version TEXT NOT NULL DEFAULT '0',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project, environment, name)
);

CREATE TABLE discovery.endpoints (
  id               TEXT PRIMARY KEY,
  project          TEXT NOT NULL,
  environment      TEXT NOT NULL,
  service          TEXT NOT NULL,
  node_id          TEXT NOT NULL,
  address_ip       TEXT NOT NULL,
  address_port     INT  NOT NULL,
  protocol         TEXT NOT NULL DEFAULT 'http',
  revision         TEXT,
  phase            TEXT NOT NULL DEFAULT 'Pending',
  resource_version TEXT NOT NULL DEFAULT '0',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (project, environment, service) REFERENCES discovery.services (project, environment, name)
);
CREATE UNIQUE INDEX idx_endpoints_pk_scope ON discovery.endpoints (project, environment, service, id);
CREATE INDEX idx_endpoints_node ON discovery.endpoints (node_id);
