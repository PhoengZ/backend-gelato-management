-- Migration 005: QUEUED becomes the initial ticket status (QUEUED -> PREPARING ->
-- READY_FOR_PICKUP -> PICKED_UP). Existing rows keep their status, so tickets
-- already PREPARING stay PREPARING. Every statement is idempotent because compose
-- also mounts this folder into docker-entrypoint-initdb.d and the service then
-- re-runs it on startup.

ALTER TABLE fulfillments ALTER COLUMN status SET DEFAULT 'QUEUED';

ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS chk_fulfillment_status;
ALTER TABLE fulfillments ADD CONSTRAINT chk_fulfillment_status
    CHECK (status IN ('QUEUED', 'PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED'));

ALTER TABLE fulfillment_status_history DROP CONSTRAINT IF EXISTS chk_history_new_status;
ALTER TABLE fulfillment_status_history ADD CONSTRAINT chk_history_new_status
    CHECK (new_status IN ('QUEUED', 'PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED'));

ALTER TABLE fulfillment_status_history DROP CONSTRAINT IF EXISTS chk_history_old_status;
ALTER TABLE fulfillment_status_history ADD CONSTRAINT chk_history_old_status
    CHECK (old_status IS NULL OR old_status IN ('QUEUED', 'PREPARING', 'READY_FOR_PICKUP', 'PICKED_UP', 'CANCELLED'));
