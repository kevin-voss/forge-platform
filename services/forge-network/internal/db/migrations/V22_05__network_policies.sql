-- 22.05: NetworkPolicy resources, environment defaults, workload placements for compile.

CREATE TABLE IF NOT EXISTS network.network_policies (
  id                 TEXT PRIMARY KEY,
  organization       TEXT NOT NULL,
  project            TEXT NOT NULL,
  environment        TEXT NOT NULL,
  name               TEXT NOT NULL,
  target_application TEXT NOT NULL,
  spec_json          JSONB NOT NULL,
  generation         INT NOT NULL DEFAULT 1,
  resource_version   BIGINT NOT NULL DEFAULT 1,
  phase              TEXT NOT NULL DEFAULT 'Ready',
  condition_type     TEXT,
  condition_status   TEXT,
  condition_reason   TEXT,
  condition_message  TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization, project, environment, name)
);

CREATE INDEX IF NOT EXISTS network_policies_target_idx
  ON network.network_policies (organization, project, environment, target_application);

CREATE TABLE IF NOT EXISTS network.environment_network_defaults (
  organization   TEXT NOT NULL,
  project        TEXT NOT NULL,
  environment    TEXT NOT NULL,
  default_policy TEXT NOT NULL DEFAULT 'allow-within-environment',
  generation     INT NOT NULL DEFAULT 1,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization, project, environment),
  CONSTRAINT environment_network_defaults_policy_chk
    CHECK (default_policy IN ('allow-within-environment', 'deny-all'))
);

-- Placement mirror (epic 08) + workload identity used by PolicyCompiler.
CREATE TABLE IF NOT EXISTS network.workload_placements (
  workload_id   TEXT PRIMARY KEY,
  organization  TEXT NOT NULL,
  project       TEXT NOT NULL,
  environment   TEXT NOT NULL,
  node_id       TEXT NOT NULL,
  application   TEXT,
  service       TEXT,
  database_name TEXT,
  queue_name    TEXT,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workload_placements_node_idx
  ON network.workload_placements (node_id);

CREATE INDEX IF NOT EXISTS workload_placements_env_idx
  ON network.workload_placements (organization, project, environment);

-- Cluster-wide rule-set generation (bumped on policy/default/placement changes).
CREATE TABLE IF NOT EXISTS network.policy_rule_generation (
  id         INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  generation BIGINT NOT NULL DEFAULT 0
);

INSERT INTO network.policy_rule_generation (id, generation)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
