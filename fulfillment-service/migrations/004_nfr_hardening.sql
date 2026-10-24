-- Migration 004: customer ownership, audit source, event-ordering tombstones and
-- retention indexes. Every statement is idempotent because compose also mounts
-- this folder into docker-entrypoint-initdb.d.

-- Owner of the order, copied from order.placed; NULL for tickets created earlier.
ALTER TABLE fulfillments ADD COLUMN IF NOT EXISTS customer_id UUID;

CREATE INDEX IF NOT EXISTS idx_fulfillments_pickup_at ON fulfillments (pickup_at);

-- CloudEvent id that caused an event-driven transition (NULL for staff actions).
ALTER TABLE fulfillment_status_history ADD COLUMN IF NOT EXISTS source_event_id UUID;

-- Orders cancelled before their order.placed was applied (e.g. after a delayed
-- retry). A later order.placed for these orders must not create a ticket.
CREATE TABLE IF NOT EXISTS cancelled_orders (
    order_id      UUID PRIMARY KEY,
    event_id      UUID        NOT NULL,
    cancelled_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cancelled_orders_cancelled_at ON cancelled_orders (cancelled_at);
CREATE INDEX IF NOT EXISTS idx_processed_events_processed_at ON processed_events (processed_at);
CREATE INDEX IF NOT EXISTS idx_outbox_published_at ON outbox (published_at) WHERE published_at IS NOT NULL;
