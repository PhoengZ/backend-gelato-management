DROP TRIGGER IF EXISTS trg_prevent_auth_audit_logs_delete ON auth_audit_logs;
DROP TRIGGER IF EXISTS trg_prevent_auth_audit_logs_update ON auth_audit_logs;
DROP FUNCTION IF EXISTS prevent_auth_audit_logs_modification();
DROP TABLE IF EXISTS auth_audit_logs;
