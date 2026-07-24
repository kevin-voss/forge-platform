-- OrderPipe schema (epic 54.01). Idempotent — safe to re-run on boot.
-- saga_events is an audit mirror for later workflow/event steps (54.04/54.05).

CREATE TABLE IF NOT EXISTS catalog_items (
    sku        TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    unit_cents INTEGER NOT NULL CHECK (unit_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS orders (
    id             TEXT PRIMARY KEY,
    customer_email TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'placed'
                   CHECK (status IN (
                       'placed', 'validated', 'charged', 'fulfilled',
                       'notified', 'failed', 'refunded'
                   )),
    total_cents    INTEGER NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS orders_status_idx ON orders (status);
CREATE INDEX IF NOT EXISTS orders_created_at_idx ON orders (created_at);

CREATE TABLE IF NOT EXISTS order_items (
    id         TEXT PRIMARY KEY,
    order_id   TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    sku        TEXT NOT NULL,
    qty        INTEGER NOT NULL CHECK (qty > 0),
    unit_cents INTEGER NOT NULL CHECK (unit_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS order_items_order_id_idx ON order_items (order_id);

CREATE TABLE IF NOT EXISTS saga_events (
    id       TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    step     TEXT NOT NULL,
    outcome  TEXT NOT NULL CHECK (outcome IN ('ok', 'retry', 'compensated')),
    at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS saga_events_order_id_idx ON saga_events (order_id);
