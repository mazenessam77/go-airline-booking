-- +goose Up
ALTER TABLE fare_offers ADD COLUMN taxes_minor BIGINT NOT NULL DEFAULT 0 CHECK (taxes_minor>=0);

CREATE TABLE idempotency_records (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    route TEXT NOT NULL CHECK (length(route)<=200),
    key TEXT NOT NULL CHECK (length(key) BETWEEN 16 AND 128),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash)=32),
    resource_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(user_id,route,key)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    locked_until TIMESTAMPTZ,
    lock_token UUID,
    delivered_at TIMESTAMPTZ,
    dead_lettered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX outbox_pending_idx ON outbox_events(next_attempt_at,id) WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
CREATE TABLE outbox_receipts (
    consumer TEXT NOT NULL,
    event_id UUID NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(consumer,event_id)
);

CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL CHECK(amount_minor>=0),
    currency VARCHAR(3) NOT NULL CHECK(currency ~ '^[A-Z]{3}$'),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','PROCESSING','SUCCEEDED','FAILED','RECONCILIATION_REQUIRED','REFUND_PENDING','REFUNDED')),
    provider_reference TEXT UNIQUE,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE payment_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    provider_key UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','SUCCEEDED','FAILED','UNKNOWN')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX payment_attempts_payment_idx ON payment_attempts(payment_id);
CREATE TABLE webhook_events (
    provider TEXT NOT NULL,
    event_id TEXT NOT NULL CHECK(length(event_id) BETWEEN 1 AND 200),
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(provider,event_id)
);
CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id UUID NOT NULL UNIQUE REFERENCES payments(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL CHECK(amount_minor>=0),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','PROCESSING','SUCCEEDED','FAILED','UNKNOWN')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE refunds;
DROP TABLE webhook_events;
DROP TABLE payment_attempts;
DROP TABLE payments;
DROP TABLE outbox_receipts;
DROP TABLE outbox_events;
DROP TABLE idempotency_records;
ALTER TABLE fare_offers DROP COLUMN taxes_minor;
