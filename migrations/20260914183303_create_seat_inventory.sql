-- +goose Up

CREATE TABLE flight_seats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    flight_instance_id UUID NOT NULL
        REFERENCES flight_instances(id)
        ON DELETE CASCADE,

    aircraft_seat_id UUID NOT NULL
        REFERENCES aircraft_seats(id)
        ON DELETE RESTRICT,

    booking_id UUID
        REFERENCES bookings(id)
        ON DELETE RESTRICT,

    state TEXT NOT NULL DEFAULT 'AVAILABLE'
        CHECK (
            state IN (
                'AVAILABLE',
                'HELD',
                'BOOKED',
                'BLOCKED'
            )
        ),

    hold_expires_at TIMESTAMPTZ,

    version BIGINT NOT NULL DEFAULT 1
        CHECK (version > 0),

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            state = 'AVAILABLE'
            AND booking_id IS NULL
            AND hold_expires_at IS NULL
        )
        OR (
            state = 'HELD'
            AND booking_id IS NOT NULL
            AND hold_expires_at IS NOT NULL
        )
        OR (
            state = 'BOOKED'
            AND booking_id IS NOT NULL
            AND hold_expires_at IS NULL
        )
        OR (
            state = 'BLOCKED'
            AND booking_id IS NULL
            AND hold_expires_at IS NULL
        )
    ),

    UNIQUE (
        flight_instance_id,
        aircraft_seat_id
    ),

    UNIQUE (
        flight_instance_id,
        id
    )
);


CREATE INDEX flight_seats_availability_idx
ON flight_seats (
    flight_instance_id,
    state
);


CREATE INDEX flight_seats_booking_idx
ON flight_seats (
    booking_id
)
WHERE booking_id IS NOT NULL;


CREATE INDEX flight_seats_expired_holds_idx
ON flight_seats (
    hold_expires_at
)
WHERE state = 'HELD';


CREATE TABLE seat_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    booking_id UUID NOT NULL,

    booking_segment_id UUID NOT NULL,

    flight_instance_id UUID NOT NULL,

    passenger_id UUID NOT NULL,

    flight_seat_id UUID NOT NULL,

    status TEXT NOT NULL DEFAULT 'HELD'
        CHECK (
            status IN (
                'HELD',
                'CONFIRMED',
                'CHECKED_IN',
                'CANCELLED',
                'EXPIRED'
            )
        ),

    hold_expires_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL
        DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            status = 'HELD'
            AND hold_expires_at IS NOT NULL
        )
        OR (
            status IN (
                'CONFIRMED',
                'CHECKED_IN'
            )
            AND hold_expires_at IS NULL
        )
        OR status IN (
            'CANCELLED',
            'EXPIRED'
        )
    ),

    CHECK (
        hold_expires_at IS NULL
        OR hold_expires_at > created_at
    ),

    FOREIGN KEY (
        booking_id,
        booking_segment_id
    )
        REFERENCES booking_segments(
            booking_id,
            id
        )
        ON DELETE CASCADE,

    FOREIGN KEY (
        flight_instance_id,
        booking_segment_id
    )
        REFERENCES booking_segments(
            flight_instance_id,
            id
        )
        ON DELETE CASCADE,

    FOREIGN KEY (
        booking_id,
        passenger_id
    )
        REFERENCES booking_passengers(
            booking_id,
            id
        )
        ON DELETE CASCADE,

    FOREIGN KEY (
        flight_instance_id,
        flight_seat_id
    )
        REFERENCES flight_seats(
            flight_instance_id,
            id
        )
        ON DELETE CASCADE
);


CREATE UNIQUE INDEX seat_assignments_active_seat_unique
ON seat_assignments (
    flight_seat_id
)
WHERE status IN (
    'HELD',
    'CONFIRMED',
    'CHECKED_IN'
);


CREATE UNIQUE INDEX seat_assignments_active_passenger_unique
ON seat_assignments (
    booking_segment_id,
    passenger_id
)
WHERE status IN (
    'HELD',
    'CONFIRMED',
    'CHECKED_IN'
);


CREATE INDEX seat_assignments_booking_idx
ON seat_assignments (
    booking_id,
    booking_segment_id
);


CREATE INDEX seat_assignments_expired_holds_idx
ON seat_assignments (
    hold_expires_at
)
WHERE status = 'HELD';


-- +goose Down

DROP TABLE IF EXISTS seat_assignments;
DROP TABLE IF EXISTS flight_seats;