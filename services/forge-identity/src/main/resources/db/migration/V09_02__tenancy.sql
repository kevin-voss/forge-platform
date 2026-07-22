-- Tenancy model (epic 09 / step 09.02): users, orgs, projects, memberships.

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  id           TEXT PRIMARY KEY,
  email        CITEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE organizations (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE org_memberships (
  org_id  TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role    TEXT NOT NULL,
  PRIMARY KEY (org_id, user_id)
);

CREATE TABLE projects (
  id       TEXT PRIMARY KEY,
  org_id   TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name     TEXT NOT NULL
);

CREATE TABLE project_memberships (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,
  PRIMARY KEY (project_id, user_id)
);

CREATE INDEX idx_org_memberships_user ON org_memberships(user_id);
CREATE INDEX idx_project_memberships_user ON project_memberships(user_id);
CREATE INDEX idx_projects_org ON projects(org_id);
