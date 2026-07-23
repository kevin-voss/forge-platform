-- Epic 22 / step 22.02: bootstrap tokens + node join handshake columns.

ALTER TABLE control.nodes
    ADD COLUMN IF NOT EXISTS wireguard_public_key TEXT,
    ADD COLUMN IF NOT EXISTS network_cidr         CIDR,
    ADD COLUMN IF NOT EXISTS network_gateway      INET,
    ADD COLUMN IF NOT EXISTS joined_at            TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS key_revoked_at       TIMESTAMPTZ;

ALTER TABLE control.nodes
    DROP CONSTRAINT IF EXISTS nodes_status_valid;

ALTER TABLE control.nodes
    ADD CONSTRAINT nodes_status_valid CHECK (
        status IN ('online', 'offline', 'draining', 'pending-network', 'joining')
    );

CREATE TABLE IF NOT EXISTS control.bootstrap_tokens (
    id               TEXT PRIMARY KEY,
    token_hash       TEXT NOT NULL UNIQUE,
    organization     TEXT NOT NULL,
    node_pool        TEXT,
    expires_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ,
    consumed_by_node TEXT,
    revoked_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT bootstrap_tokens_id_not_blank CHECK (length(btrim(id)) > 0),
    CONSTRAINT bootstrap_tokens_org_not_blank CHECK (length(btrim(organization)) > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS bootstrap_tokens_single_use
    ON control.bootstrap_tokens (id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_bootstrap_tokens_expires_at
    ON control.bootstrap_tokens (expires_at);
