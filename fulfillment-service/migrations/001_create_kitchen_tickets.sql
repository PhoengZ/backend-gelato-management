-- Fulfillment Service: kitchen_tickets table
-- Matches the "kitchen ticket" resource used for the REST API assignment.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS kitchen_tickets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      VARCHAR(64)  NOT NULL,
    pickup_slot   VARCHAR(32)  NOT NULL,        -- e.g. "14:00-14:15"
    queue_number  INT          NOT NULL,        -- scoped to pickup_slot
    status        VARCHAR(32)  NOT NULL DEFAULT 'PREPARING',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT chk_status CHECK (status IN ('PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP'))
);

CREATE INDEX IF NOT EXISTS idx_kitchen_tickets_slot ON kitchen_tickets (pickup_slot);
CREATE INDEX IF NOT EXISTS idx_kitchen_tickets_status ON kitchen_tickets (status);

-- sample seed rows (optional, handy for the demo video)
INSERT INTO kitchen_tickets (order_id, pickup_slot, queue_number, status)
VALUES
    ('order-1001', '14:00-14:15', 1, 'PREPARING'),
    ('order-1002', '14:00-14:15', 2, 'READY_FOR_PICKUP')
ON CONFLICT DO NOTHING;
