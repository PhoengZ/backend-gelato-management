-- Migration: 000003_create_fulfillment_history.down.sql

DROP TRIGGER IF EXISTS trg_prevent_fulfillment_history_mutation ON fulfillment_status_history;
DROP FUNCTION IF EXISTS prevent_fulfillment_history_mutation();
DROP TABLE IF EXISTS fulfillment_status_history;
