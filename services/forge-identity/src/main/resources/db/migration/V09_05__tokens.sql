-- API tokens + service accounts (epic 09 / step 09.05).

CREATE TABLE service_accounts (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  role       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project_id, name)
);

CREATE INDEX idx_service_accounts_project ON service_accounts(project_id);

CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  prefix       TEXT NOT NULL,
  token_hash   TEXT UNIQUE NOT NULL,
  owner_type   TEXT NOT NULL,
  owner_id     TEXT NOT NULL,
  project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  role         TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ,
  CONSTRAINT api_tokens_owner_type_chk CHECK (owner_type IN ('user', 'service_account'))
);

CREATE INDEX idx_api_tokens_owner ON api_tokens(owner_type, owner_id);
CREATE INDEX idx_api_tokens_project ON api_tokens(project_id);
CREATE INDEX idx_api_tokens_active ON api_tokens(expires_at)
  WHERE revoked_at IS NULL;
