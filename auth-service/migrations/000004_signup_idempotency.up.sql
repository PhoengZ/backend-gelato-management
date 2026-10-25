CREATE TABLE auth_signup_idempotency (
    key UUID PRIMARY KEY CHECK (key <> '00000000-0000-0000-0000-000000000000'::uuid),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    password_hash TEXT NOT NULL,
    response JSONB CHECK (response IS NULL OR jsonb_typeof(response) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() + interval '24 hours',
    CHECK (expires_at > created_at)
);
CREATE INDEX auth_signup_idempotency_expiry ON auth_signup_idempotency(expires_at);
