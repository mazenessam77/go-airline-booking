-- +goose Up

CREATE TABLE airports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    iata_code VARCHAR(3) NOT NULL UNIQUE
        CHECK (iata_code ~ '^[A-Z]{3}$'),

    name TEXT NOT NULL,

    city TEXT NOT NULL,

    country_code VARCHAR(2) NOT NULL
        CHECK (country_code ~ '^[A-Z]{2}$'),

    timezone TEXT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE aircraft_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    manufacturer TEXT NOT NULL,

    model TEXT NOT NULL,

    code VARCHAR(10) NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE aircraft (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    aircraft_type_id UUID NOT NULL
        REFERENCES aircraft_types(id)
        ON DELETE RESTRICT,

    tail_number TEXT NOT NULL UNIQUE,

    status TEXT NOT NULL DEFAULT 'ACTIVE'
        CHECK (
            status IN (
                'ACTIVE',
                'MAINTENANCE',
                'RETIRED'
            )
        ),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE aircraft_seats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    aircraft_type_id UUID NOT NULL
        REFERENCES aircraft_types(id)
        ON DELETE CASCADE,

    seat_number VARCHAR(5) NOT NULL,

    row_number INTEGER NOT NULL
        CHECK (row_number > 0),

    seat_column VARCHAR(2) NOT NULL,

    cabin TEXT NOT NULL
        CHECK (
            cabin IN (
                'ECONOMY',
                'PREMIUM_ECONOMY',
                'BUSINESS',
                'FIRST'
            )
        ),

    is_window BOOLEAN NOT NULL DEFAULT FALSE,

    is_aisle BOOLEAN NOT NULL DEFAULT FALSE,

    UNIQUE (
        aircraft_type_id,
        seat_number
    )
);


CREATE TABLE flights (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    airline_code VARCHAR(3) NOT NULL,

    flight_number VARCHAR(8) NOT NULL,

    origin_airport_id UUID NOT NULL
        REFERENCES airports(id)
        ON DELETE RESTRICT,

    destination_airport_id UUID NOT NULL
        REFERENCES airports(id)
        ON DELETE RESTRICT,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        origin_airport_id <> destination_airport_id
    ),

    UNIQUE (
        airline_code,
        flight_number,
        origin_airport_id,
        destination_airport_id
    )
);


CREATE INDEX flights_route_idx
ON flights (
    origin_airport_id,
    destination_airport_id
);


CREATE TABLE flight_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    flight_id UUID NOT NULL
        REFERENCES flights(id)
        ON DELETE RESTRICT,

    aircraft_id UUID NOT NULL
        REFERENCES aircraft(id)
        ON DELETE RESTRICT,

    scheduled_departure_at TIMESTAMPTZ NOT NULL,

    scheduled_arrival_at TIMESTAMPTZ NOT NULL,

    actual_departure_at TIMESTAMPTZ,

    actual_arrival_at TIMESTAMPTZ,

    status TEXT NOT NULL DEFAULT 'SCHEDULED'
        CHECK (
            status IN (
                'SCHEDULED',
                'BOARDING',
                'DEPARTED',
                'ARRIVED',
                'DELAYED',
                'CANCELLED'
            )
        ),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        scheduled_arrival_at > scheduled_departure_at
    ),

    UNIQUE (
        flight_id,
        scheduled_departure_at
    )
);


CREATE INDEX flight_instances_departure_idx
ON flight_instances (
    scheduled_departure_at,
    status
);


-- +goose Down

DROP TABLE IF EXISTS flight_instances;
DROP TABLE IF EXISTS flights;
DROP TABLE IF EXISTS aircraft_seats;
DROP TABLE IF EXISTS aircraft;
DROP TABLE IF EXISTS aircraft_types;
DROP TABLE IF EXISTS airports;