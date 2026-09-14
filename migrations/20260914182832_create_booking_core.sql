-- +goose Up

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    email TEXT NOT NULL,

    password_hash TEXT NOT NULL,

    first_name TEXT NOT NULL,

    last_name TEXT NOT NULL,

    role TEXT NOT NULL DEFAULT 'CUSTOMER'
        CHECK (
            role IN (
                'CUSTOMER',
                'AGENT',
                'ADMIN'
            )
        ),

    status TEXT NOT NULL DEFAULT 'ACTIVE'
        CHECK (
            status IN (
                'ACTIVE',
                'SUSPENDED',
                'DELETED'
            )
        ),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        char_length(email) BETWEEN 3 AND 320
    )
);


CREATE UNIQUE INDEX users_email_unique
ON users (
    LOWER(email)
);


CREATE TABLE fare_offers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    flight_instance_id UUID NOT NULL
        REFERENCES flight_instances(id)
        ON DELETE CASCADE,

    fare_code VARCHAR(20) NOT NULL,

    cabin TEXT NOT NULL
        CHECK (
            cabin IN (
                'ECONOMY',
                'PREMIUM_ECONOMY',
                'BUSINESS',
                'FIRST'
            )
        ),

    price_minor BIGINT NOT NULL
        CHECK (price_minor >= 0),

    currency VARCHAR(3) NOT NULL
        CHECK (currency ~ '^[A-Z]{3}$'),

    refundable BOOLEAN NOT NULL DEFAULT FALSE,

    baggage_allowance_kg INTEGER NOT NULL DEFAULT 0
        CHECK (baggage_allowance_kg >= 0),

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    valid_from TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    valid_until TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        valid_until IS NULL
        OR valid_until > valid_from
    ),

    UNIQUE (
        flight_instance_id,
        fare_code
    ),

    UNIQUE (
        flight_instance_id,
        id
    )
);


CREATE INDEX fare_offers_lookup_idx
ON fare_offers (
    flight_instance_id,
    cabin,
    is_active
);


CREATE TABLE price_quotes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID
        REFERENCES users(id)
        ON DELETE SET NULL,

    request_hash VARCHAR(64) NOT NULL
        CHECK (
            request_hash ~ '^[a-f0-9]{64}$'
        ),

    status TEXT NOT NULL DEFAULT 'ACTIVE'
        CHECK (
            status IN (
                'ACTIVE',
                'USED',
                'EXPIRED'
            )
        ),

    total_minor BIGINT NOT NULL
        CHECK (total_minor >= 0),

    currency VARCHAR(3) NOT NULL
        CHECK (currency ~ '^[A-Z]{3}$'),

    expires_at TIMESTAMPTZ NOT NULL,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        expires_at > created_at
    )
);


CREATE INDEX price_quotes_active_expiration_idx
ON price_quotes (
    expires_at
)
WHERE status = 'ACTIVE';


CREATE TABLE price_quote_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    price_quote_id UUID NOT NULL
        REFERENCES price_quotes(id)
        ON DELETE CASCADE,

    flight_instance_id UUID NOT NULL,

    fare_offer_id UUID NOT NULL,

    passenger_count INTEGER NOT NULL
        CHECK (
            passenger_count BETWEEN 1 AND 9
        ),

    unit_price_minor BIGINT NOT NULL
        CHECK (unit_price_minor >= 0),

    taxes_minor BIGINT NOT NULL DEFAULT 0
        CHECK (taxes_minor >= 0),

    total_minor BIGINT NOT NULL
        CHECK (total_minor >= 0),

    currency VARCHAR(3) NOT NULL
        CHECK (currency ~ '^[A-Z]{3}$'),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (
        flight_instance_id,
        fare_offer_id
    )
        REFERENCES fare_offers(
            flight_instance_id,
            id
        )
        ON DELETE RESTRICT,

    UNIQUE (
        price_quote_id,
        flight_instance_id
    )
);


CREATE INDEX price_quote_items_quote_idx
ON price_quote_items (
    price_quote_id
);


CREATE TABLE bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID
        REFERENCES users(id)
        ON DELETE RESTRICT,

    price_quote_id UUID NOT NULL UNIQUE
        REFERENCES price_quotes(id)
        ON DELETE RESTRICT,

    pnr VARCHAR(8) NOT NULL UNIQUE
        CHECK (
            pnr ~ '^[A-Z0-9]{6,8}$'
        ),

    manage_token_hash BYTEA,

    status TEXT NOT NULL DEFAULT 'DRAFT'
        CHECK (
            status IN (
                'DRAFT',
                'HELD',
                'PAYMENT_PENDING',
                'CONFIRMED',
                'TICKETED',
                'EXPIRED',
                'CANCELLED',
                'REFUNDED'
            )
        ),

    total_minor BIGINT NOT NULL
        CHECK (total_minor >= 0),

    currency VARCHAR(3) NOT NULL
        CHECK (currency ~ '^[A-Z]{3}$'),

    hold_expires_at TIMESTAMPTZ,

    version BIGINT NOT NULL DEFAULT 1
        CHECK (version > 0),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            user_id IS NOT NULL
            AND manage_token_hash IS NULL
        )
        OR (
            user_id IS NULL
            AND manage_token_hash IS NOT NULL
        )
    )
);


CREATE INDEX bookings_user_created_idx
ON bookings (
    user_id,
    created_at DESC
);


CREATE INDEX bookings_active_holds_idx
ON bookings (
    hold_expires_at
)
WHERE status IN (
    'HELD',
    'PAYMENT_PENDING'
);


CREATE TABLE booking_passengers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    booking_id UUID NOT NULL
        REFERENCES bookings(id)
        ON DELETE CASCADE,

    passenger_type TEXT NOT NULL
        CHECK (
            passenger_type IN (
                'ADULT',
                'CHILD',
                'INFANT'
            )
        ),

    first_name TEXT NOT NULL,

    last_name TEXT NOT NULL,

    date_of_birth DATE,

    nationality VARCHAR(2)
        CHECK (
            nationality IS NULL
            OR nationality ~ '^[A-Z]{2}$'
        ),

    travel_document_ciphertext BYTEA,

    travel_document_key_id TEXT,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            travel_document_ciphertext IS NULL
            AND travel_document_key_id IS NULL
        )
        OR (
            travel_document_ciphertext IS NOT NULL
            AND travel_document_key_id IS NOT NULL
        )
    ),

    UNIQUE (
        booking_id,
        id
    )
);


CREATE INDEX booking_passengers_booking_idx
ON booking_passengers (
    booking_id
);


CREATE TABLE booking_segments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    booking_id UUID NOT NULL
        REFERENCES bookings(id)
        ON DELETE CASCADE,

    flight_instance_id UUID NOT NULL,

    fare_offer_id UUID NOT NULL,

    status TEXT NOT NULL DEFAULT 'DRAFT'
        CHECK (
            status IN (
                'DRAFT',
                'HELD',
                'CONFIRMED',
                'FLOWN',
                'CANCELLED'
            )
        ),

    price_minor BIGINT NOT NULL
        CHECK (price_minor >= 0),

    taxes_minor BIGINT NOT NULL DEFAULT 0
        CHECK (taxes_minor >= 0),

    currency VARCHAR(3) NOT NULL
        CHECK (currency ~ '^[A-Z]{3}$'),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (
        flight_instance_id,
        fare_offer_id
    )
        REFERENCES fare_offers(
            flight_instance_id,
            id
        )
        ON DELETE RESTRICT,

    UNIQUE (
        booking_id,
        flight_instance_id
    ),

    UNIQUE (
        booking_id,
        id
    ),

    UNIQUE (
        flight_instance_id,
        id
    )
);


CREATE INDEX booking_segments_booking_idx
ON booking_segments (
    booking_id
);


CREATE INDEX booking_segments_flight_idx
ON booking_segments (
    flight_instance_id
);


-- +goose Down

DROP TABLE IF EXISTS booking_segments;
DROP TABLE IF EXISTS booking_passengers;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS price_quote_items;
DROP TABLE IF EXISTS price_quotes;
DROP TABLE IF EXISTS fare_offers;
DROP TABLE IF EXISTS users;