-- Fulfillment Service schema aligned with contracts/openapi/fulfillment-service.v1.yaml.
-- Replaces the assignment-era kitchen_tickets table (001) and its seed rows.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

DROP TABLE IF EXISTS kitchen_tickets;

-- 15-minute pickup slots, created on demand. next_queue is the next queue
-- number to hand out inside the slot.
CREATE TABLE IF NOT EXISTS pickup_slots (
    id          UUID PRIMARY KEY,
    starts_at   TIMESTAMPTZ NOT NULL UNIQUE,
    next_queue  INT         NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS fulfillments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID         NOT NULL UNIQUE,
    pickup_slot_id  UUID         NOT NULL REFERENCES pickup_slots (id),
    queue_seq       INT          NOT NULL,
    queue_number    VARCHAR(16)  NOT NULL,
    pickup_at       TIMESTAMPTZ  NOT NULL,
    status          VARCHAR(32)  NOT NULL DEFAULT 'PREPARING',
    items           JSONB        NOT NULL DEFAULT '[]',
    picked_up_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT chk_fulfillment_status
        CHECK (status IN ('PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED')),
    CONSTRAINT uq_fulfillment_slot_queue UNIQUE (pickup_slot_id, queue_seq)
);

CREATE INDEX IF NOT EXISTS idx_fulfillments_status ON fulfillments (status);

-- Consumer deduplication: one row per processed CloudEvent id.
CREATE TABLE IF NOT EXISTS processed_events (
    event_id      UUID PRIMARY KEY,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Transactional outbox: rows are written in the same transaction as the
-- state change and published to RabbitMQ by the relay.
CREATE TABLE IF NOT EXISTS outbox (
    id            UUID PRIMARY KEY,
    routing_key   VARCHAR(128) NOT NULL,
    payload       JSONB        NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    published_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;
