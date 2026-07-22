-- Auth credentials + sessions (epic 09 / step 09.03).

CREATE TABLE credentials (
  user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  hash       TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT UNIQUE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_active ON sessions(expires_at)
  WHERE revoked_at IS NULL;

CREATE TABLE login_attempts (
  email   CITEXT NOT NULL,
  at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  success BOOLEAN NOT NULL
);

CREATE INDEX idx_login_attempts_email_at ON login_attempts(email, at DESC);
