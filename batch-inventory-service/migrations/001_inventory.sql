CREATE TABLE batches (
 id uuid PRIMARY KEY, flavor_id uuid NOT NULL, production_date date NOT NULL,
 expires_at timestamptz NOT NULL, initial_portions bigint NOT NULL CHECK (initial_portions BETWEEN 1 AND 2147483647),
 available_portions bigint NOT NULL CHECK (available_portions >= 0),
 reserved_portions bigint NOT NULL DEFAULT 0 CHECK (reserved_portions >= 0),
 sold_portions bigint NOT NULL DEFAULT 0 CHECK (sold_portions >= 0),
 wasted_portions bigint NOT NULL DEFAULT 0 CHECK (wasted_portions >= 0),
 unit_cost_minor bigint NOT NULL CHECK (unit_cost_minor >= 0), currency text NOT NULL CHECK (currency = 'THB'),
 status text NOT NULL CHECK (status IN ('ACTIVE','EXHAUSTED','EXPIRED','ARCHIVED')),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 CHECK (initial_portions = available_portions + reserved_portions + sold_portions + wasted_portions)
);
CREATE INDEX batches_fefo ON batches (flavor_id, expires_at, created_at, id);
CREATE TABLE reservations (
 id uuid PRIMARY KEY, order_id uuid NOT NULL UNIQUE,
 status text NOT NULL CHECK (status IN ('ACTIVE','CONFIRMED','RELEASED','EXPIRED')),
 created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 release_reason text NOT NULL DEFAULT '', CHECK (expires_at > created_at)
);
CREATE INDEX reservation_expiry ON reservations (expires_at) WHERE status = 'ACTIVE';
CREATE TABLE reservation_allocations (
 reservation_id uuid NOT NULL REFERENCES reservations(id), batch_id uuid NOT NULL REFERENCES batches(id),
 flavor_id uuid NOT NULL, portions bigint NOT NULL CHECK (portions > 0), position integer NOT NULL,
 PRIMARY KEY (reservation_id, batch_id), UNIQUE (reservation_id, position)
);
CREATE TABLE inventory_movements (
 id uuid PRIMARY KEY, batch_id uuid NOT NULL REFERENCES batches(id), reservation_id uuid REFERENCES reservations(id),
 kind text NOT NULL CHECK (kind IN ('PRODUCED','RESERVED','CONFIRMED','RELEASED','WASTED')),
 portions bigint NOT NULL CHECK (portions > 0), occurred_at timestamptz NOT NULL
);
CREATE TABLE waste_records (
 id uuid PRIMARY KEY, batch_id uuid NOT NULL REFERENCES batches(id), flavor_id uuid NOT NULL,
 portions bigint NOT NULL CHECK (portions > 0), reason text NOT NULL CHECK (reason IN ('EXPIRED','DAMAGED','QUALITY','ADJUSTMENT')),
 note text NOT NULL, actor_id uuid, cost_lost_minor bigint NOT NULL CHECK (cost_lost_minor >= 0),
 currency text NOT NULL CHECK (currency = 'THB'), recorded_at timestamptz NOT NULL
);
CREATE TABLE idempotency_records (
 operation text NOT NULL, key uuid NOT NULL, request_hash bytea NOT NULL, response jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY (operation, key)
);
CREATE TABLE outbox_events (
 id uuid PRIMARY KEY, payload jsonb NOT NULL, created_at timestamptz NOT NULL,
 published_at timestamptz, attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX outbox_pending ON outbox_events(next_attempt_at, created_at) WHERE published_at IS NULL;
