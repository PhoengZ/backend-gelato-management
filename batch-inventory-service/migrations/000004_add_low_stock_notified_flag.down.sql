ALTER TABLE batches DROP COLUMN IF EXISTS expiring_notified_at;
ALTER TABLE batches DROP COLUMN IF EXISTS low_stock_notified_at;
