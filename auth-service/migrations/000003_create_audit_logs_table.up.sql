CREATE TABLE auth_audit_logs (
    id UUID PRIMARY KEY,
    event_type VARCHAR(50) NOT NULL,
    user_id UUID,
    ip_address VARCHAR(45) NOT NULL DEFAULT '',
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_auth_audit_logs_user_id ON auth_audit_logs(user_id);
CREATE INDEX idx_auth_audit_logs_event_type ON auth_audit_logs(event_type);
CREATE INDEX idx_auth_audit_logs_created_at ON auth_audit_logs(created_at);

-- Trigger preventing UPDATE and DELETE operations to guarantee immutability (append-only)
CREATE OR REPLACE FUNCTION prevent_auth_audit_logs_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'auth_audit_logs is append-only and cannot be modified or deleted';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_auth_audit_logs_update
BEFORE UPDATE ON auth_audit_logs
FOR EACH ROW
EXECUTE FUNCTION prevent_auth_audit_logs_modification();

CREATE TRIGGER trg_prevent_auth_audit_logs_delete
BEFORE DELETE ON auth_audit_logs
FOR EACH ROW
EXECUTE FUNCTION prevent_auth_audit_logs_modification();
