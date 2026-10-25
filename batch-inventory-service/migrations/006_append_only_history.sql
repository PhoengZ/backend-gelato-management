-- Preserve all historical rows, including unknown actors. Normal application
-- writes append evidence; corrections must be new compensating operations.
CREATE FUNCTION prevent_inventory_history_modification()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Inventory history is append-only' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER inventory_movements_append_only
BEFORE UPDATE OR DELETE ON inventory_movements
FOR EACH ROW EXECUTE FUNCTION prevent_inventory_history_modification();
CREATE TRIGGER inventory_movements_no_truncate
BEFORE TRUNCATE ON inventory_movements
FOR EACH STATEMENT EXECUTE FUNCTION prevent_inventory_history_modification();

CREATE TRIGGER waste_records_append_only
BEFORE UPDATE OR DELETE ON waste_records
FOR EACH ROW EXECUTE FUNCTION prevent_inventory_history_modification();
CREATE TRIGGER waste_records_no_truncate
BEFORE TRUNCATE ON waste_records
FOR EACH STATEMENT EXECUTE FUNCTION prevent_inventory_history_modification();

ALTER TABLE inventory_movements ENABLE ALWAYS TRIGGER inventory_movements_append_only;
ALTER TABLE inventory_movements ENABLE ALWAYS TRIGGER inventory_movements_no_truncate;
ALTER TABLE waste_records ENABLE ALWAYS TRIGGER waste_records_append_only;
ALTER TABLE waste_records ENABLE ALWAYS TRIGGER waste_records_no_truncate;
