-- Migration 003: create immutable fulfillment_status_history table
-- Matches 000003_create_fulfillment_history.up.sql

CREATE TABLE IF NOT EXISTS fulfillment_status_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fulfillment_id  UUID NOT NULL REFERENCES fulfillments (id) ON DELETE CASCADE,
    old_status      VARCHAR(32),
    new_status      VARCHAR(32) NOT NULL,
    actor_id        UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_history_new_status
        CHECK (new_status IN ('PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED')),
    CONSTRAINT chk_history_old_status
        CHECK (old_status IS NULL OR old_status IN ('PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED'))
);

CREATE INDEX IF NOT EXISTS idx_fulfillment_history_fulfillment_id
    ON fulfillment_status_history (fulfillment_id);

CREATE INDEX IF NOT EXISTS idx_fulfillment_history_created_at
    ON fulfillment_status_history (created_at);

-- Prevent update or delete to enforce immutability
CREATE OR REPLACE FUNCTION prevent_fulfillment_history_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'fulfillment_status_history is an immutable audit log; updates and deletes are prohibited';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_fulfillment_history_mutation ON fulfillment_status_history;
CREATE TRIGGER trg_prevent_fulfillment_history_mutation
BEFORE UPDATE OR DELETE ON fulfillment_status_history
FOR EACH ROW
EXECUTE FUNCTION prevent_fulfillment_history_mutation();
