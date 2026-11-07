-- Store cancellation of a paid order returns its confirmed portions. Existing
-- rows keep their values; only the allowed status and movement kinds widen.
-- Adding a constraint does not fire the append-only row triggers.
ALTER TABLE reservations DROP CONSTRAINT IF EXISTS reservations_status_check;
ALTER TABLE reservations ADD CONSTRAINT reservations_status_check
  CHECK (status IN ('ACTIVE','CONFIRMED','RELEASED','EXPIRED','RETURNED'));
ALTER TABLE inventory_movements DROP CONSTRAINT IF EXISTS inventory_movements_kind_check;
ALTER TABLE inventory_movements ADD CONSTRAINT inventory_movements_kind_check
  CHECK (kind IN ('PRODUCED','RESERVED','CONFIRMED','RELEASED','WASTED','RETURNED'));
