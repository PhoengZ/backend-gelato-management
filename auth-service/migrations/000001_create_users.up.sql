CREATE TABLE users (
    id UUID PRIMARY KEY,
    email VARCHAR(254) NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    password_hash VARCHAR(100) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'CUSTOMER',
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_email_normalized CHECK (email = LOWER(email)),
    CONSTRAINT users_role_valid CHECK (role IN ('CUSTOMER', 'STAFF', 'MANAGER')),
    CONSTRAINT users_status_valid CHECK (status IN ('ACTIVE', 'DISABLED'))
);

CREATE UNIQUE INDEX users_email_unique ON users (email);
