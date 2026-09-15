package booking

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

type seatRaceFixture struct {
	UserOneID        string
	UserTwoID        string
	FlightInstanceID string
	FlightSeatID     string
	BookingOneID     string
	SegmentOneID     string
	PassengerOneID   string
	BookingTwoID     string
	SegmentTwoID     string
	PassengerTwoID   string
}

type holdAttempt struct {
	hold SeatHold
	err  error
}

func TestHoldSeatsPreventsDoubleBooking(
	t *testing.T,
) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	testContext, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()

	pool, err := database.NewPostgresPool(
		testContext,
		databaseURL,
		5,
		1,
	)
	if err != nil {
		t.Fatalf(
			"connect to test database: %v",
			err,
		)
	}
	t.Cleanup(pool.Close)
	testsupport.Lock(t, pool)

	databaseName :=
		pool.Config().ConnConfig.Database

	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf(
			"refusing to use non-test database: %s",
			databaseName,
		)
	}

	if err := clearBookingTestDatabase(
		testContext,
		pool,
	); err != nil {
		t.Fatalf(
			"clear test database: %v",
			err,
		)
	}

	defer func() {
		cleanupContext, cleanupCancel :=
			context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
		defer cleanupCancel()

		if err := clearBookingTestDatabase(
			cleanupContext,
			pool,
		); err != nil {
			t.Errorf(
				"clean test database: %v",
				err,
			)
		}
	}()

	fixture := createSeatRaceFixture(
		t,
		testContext,
		pool,
	)

	store := NewStore(pool)

	attempts := []HoldSeatsParams{
		{
			UserID:           fixture.UserOneID,
			BookingID:        fixture.BookingOneID,
			BookingSegmentID: fixture.SegmentOneID,
			FlightInstanceID: fixture.FlightInstanceID,
			Seats: []SeatSelection{
				{
					PassengerID:  fixture.PassengerOneID,
					FlightSeatID: fixture.FlightSeatID,
				},
			},
		},
		{
			UserID:           fixture.UserTwoID,
			BookingID:        fixture.BookingTwoID,
			BookingSegmentID: fixture.SegmentTwoID,
			FlightInstanceID: fixture.FlightInstanceID,
			Seats: []SeatSelection{
				{
					PassengerID:  fixture.PassengerTwoID,
					FlightSeatID: fixture.FlightSeatID,
				},
			},
		},
	}

	start := make(chan struct{})
	results := make(
		chan holdAttempt,
		len(attempts),
	)

	for _, params := range attempts {
		params := params

		go func() {
			<-start

			hold, err := store.HoldSeats(
				testContext,
				params,
			)

			results <- holdAttempt{
				hold: hold,
				err:  err,
			}
		}()
	}

	close(start)

	successCount := 0
	unavailableCount := 0
	winnerBookingID := ""

	for range attempts {
		result := <-results

		switch {
		case result.err == nil:
			successCount++
			winnerBookingID =
				result.hold.BookingID

			if !result.hold.ExpiresAt.After(
				time.Now(),
			) {
				t.Errorf(
					"successful hold has expired",
				)
			}

		case errors.Is(
			result.err,
			ErrSeatUnavailable,
		):
			unavailableCount++

		default:
			t.Errorf(
				"unexpected hold error: %v",
				result.err,
			)
		}
	}

	if successCount != 1 {
		t.Fatalf(
			"expected one successful hold, got %d",
			successCount,
		)
	}

	if unavailableCount != 1 {
		t.Fatalf(
			"expected one unavailable result, got %d",
			unavailableCount,
		)
	}

	var seatState string
	var seatBookingID string
	var seatExpiresAt time.Time

	err = pool.QueryRow(
		testContext,
		`
			SELECT
				state,
				booking_id::text,
				hold_expires_at
			FROM flight_seats
			WHERE id = $1
		`,
		fixture.FlightSeatID,
	).Scan(
		&seatState,
		&seatBookingID,
		&seatExpiresAt,
	)
	if err != nil {
		t.Fatalf(
			"read held seat: %v",
			err,
		)
	}

	if seatState != "HELD" {
		t.Errorf(
			"expected seat state HELD, got %s",
			seatState,
		)
	}

	if seatBookingID != winnerBookingID {
		t.Errorf(
			"seat belongs to %s, winner is %s",
			seatBookingID,
			winnerBookingID,
		)
	}

	if !seatExpiresAt.After(time.Now()) {
		t.Errorf("seat hold is already expired")
	}

	var activeAssignmentCount int

	err = pool.QueryRow(
		testContext,
		`
			SELECT COUNT(*)
			FROM seat_assignments
			WHERE flight_seat_id = $1
			  AND status IN (
				'HELD',
				'CONFIRMED',
				'CHECKED_IN'
			  )
		`,
		fixture.FlightSeatID,
	).Scan(&activeAssignmentCount)
	if err != nil {
		t.Fatalf(
			"count active assignments: %v",
			err,
		)
	}

	if activeAssignmentCount != 1 {
		t.Errorf(
			"expected one active assignment, got %d",
			activeAssignmentCount,
		)
	}
}

func createSeatRaceFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) seatRaceFixture {
	t.Helper()

	originAirportID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO airports (
				iata_code,
				name,
				city,
				country_code,
				timezone
			)
			VALUES (
				'AAA',
				'Test Origin Airport',
				'Origin City',
				'US',
				'UTC'
			)
			RETURNING id::text
		`,
	)

	destinationAirportID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO airports (
				iata_code,
				name,
				city,
				country_code,
				timezone
			)
			VALUES (
				'BBB',
				'Test Destination Airport',
				'Destination City',
				'US',
				'UTC'
			)
			RETURNING id::text
		`,
	)

	aircraftTypeID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO aircraft_types (
				manufacturer,
				model,
				code
			)
			VALUES (
				'Test Manufacturer',
				'Test Model',
				'TEST-A320'
			)
			RETURNING id::text
		`,
	)

	aircraftID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO aircraft (
				aircraft_type_id,
				tail_number
			)
			VALUES ($1, 'TEST-N001')
			RETURNING id::text
		`,
		aircraftTypeID,
	)

	aircraftSeatID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO aircraft_seats (
				aircraft_type_id,
				seat_number,
				row_number,
				seat_column,
				cabin,
				is_window
			)
			VALUES (
				$1,
				'1A',
				1,
				'A',
				'ECONOMY',
				TRUE
			)
			RETURNING id::text
		`,
		aircraftTypeID,
	)

	flightID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO flights (
				airline_code,
				flight_number,
				origin_airport_id,
				destination_airport_id
			)
			VALUES (
				'TST',
				'100',
				$1,
				$2
			)
			RETURNING id::text
		`,
		originAirportID,
		destinationAirportID,
	)

	flightInstanceID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO flight_instances (
				flight_id,
				aircraft_id,
				scheduled_departure_at,
				scheduled_arrival_at
			)
			VALUES (
				$1,
				$2,
				CURRENT_TIMESTAMP
					+ INTERVAL '24 hours',
				CURRENT_TIMESTAMP
					+ INTERVAL '27 hours'
			)
			RETURNING id::text
		`,
		flightID,
		aircraftID,
	)

	fareOfferID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO fare_offers (
				flight_instance_id,
				fare_code,
				cabin,
				price_minor,
				currency
			)
			VALUES (
				$1,
				'TEST-ECONOMY',
				'ECONOMY',
				10000,
				'USD'
			)
			RETURNING id::text
		`,
		flightInstanceID,
	)

	userOneID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO users (
				email,
				password_hash,
				first_name,
				last_name
			)
			VALUES (
				'first@example.test',
				'test-password-hash',
				'First',
				'Customer'
			)
			RETURNING id::text
		`,
	)

	userTwoID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO users (
				email,
				password_hash,
				first_name,
				last_name
			)
			VALUES (
				'second@example.test',
				'test-password-hash',
				'Second',
				'Customer'
			)
			RETURNING id::text
		`,
	)

	quoteOneID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO price_quotes (
				user_id,
				request_hash,
				total_minor,
				currency,
				expires_at
			)
			VALUES (
				$1,
				$2,
				10000,
				'USD',
				CURRENT_TIMESTAMP
					+ INTERVAL '30 minutes'
			)
			RETURNING id::text
		`,
		userOneID,
		strings.Repeat("a", 64),
	)

	quoteTwoID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO price_quotes (
				user_id,
				request_hash,
				total_minor,
				currency,
				expires_at
			)
			VALUES (
				$1,
				$2,
				10000,
				'USD',
				CURRENT_TIMESTAMP
					+ INTERVAL '30 minutes'
			)
			RETURNING id::text
		`,
		userTwoID,
		strings.Repeat("b", 64),
	)

	bookingOneID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO bookings (
				user_id,
				price_quote_id,
				pnr,
				total_minor,
				currency
			)
			VALUES (
				$1,
				$2,
				'TEST01',
				10000,
				'USD'
			)
			RETURNING id::text
		`,
		userOneID,
		quoteOneID,
	)

	bookingTwoID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO bookings (
				user_id,
				price_quote_id,
				pnr,
				total_minor,
				currency
			)
			VALUES (
				$1,
				$2,
				'TEST02',
				10000,
				'USD'
			)
			RETURNING id::text
		`,
		userTwoID,
		quoteTwoID,
	)

	passengerOneID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO booking_passengers (
				booking_id,
				passenger_type,
				first_name,
				last_name
			)
			VALUES (
				$1,
				'ADULT',
				'First',
				'Passenger'
			)
			RETURNING id::text
		`,
		bookingOneID,
	)

	passengerTwoID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO booking_passengers (
				booking_id,
				passenger_type,
				first_name,
				last_name
			)
			VALUES (
				$1,
				'ADULT',
				'Second',
				'Passenger'
			)
			RETURNING id::text
		`,
		bookingTwoID,
	)

	segmentOneID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO booking_segments (
				booking_id,
				flight_instance_id,
				fare_offer_id,
				price_minor,
				taxes_minor,
				currency
			)
			VALUES (
				$1,
				$2,
				$3,
				9000,
				1000,
				'USD'
			)
			RETURNING id::text
		`,
		bookingOneID,
		flightInstanceID,
		fareOfferID,
	)

	segmentTwoID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO booking_segments (
				booking_id,
				flight_instance_id,
				fare_offer_id,
				price_minor,
				taxes_minor,
				currency
			)
			VALUES (
				$1,
				$2,
				$3,
				9000,
				1000,
				'USD'
			)
			RETURNING id::text
		`,
		bookingTwoID,
		flightInstanceID,
		fareOfferID,
	)

	flightSeatID := insertTestID(
		t,
		ctx,
		pool,
		`
			INSERT INTO flight_seats (
				flight_instance_id,
				aircraft_seat_id
			)
			VALUES ($1, $2)
			RETURNING id::text
		`,
		flightInstanceID,
		aircraftSeatID,
	)

	return seatRaceFixture{
		UserOneID:        userOneID,
		UserTwoID:        userTwoID,
		FlightInstanceID: flightInstanceID,
		FlightSeatID:     flightSeatID,
		BookingOneID:     bookingOneID,
		SegmentOneID:     segmentOneID,
		PassengerOneID:   passengerOneID,
		BookingTwoID:     bookingTwoID,
		SegmentTwoID:     segmentTwoID,
		PassengerTwoID:   passengerTwoID,
	}
}

func insertTestID(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	query string,
	arguments ...any,
) string {
	t.Helper()

	var id string

	if err := pool.QueryRow(
		ctx,
		query,
		arguments...,
	).Scan(&id); err != nil {
		t.Fatalf(
			"insert test record: %v",
			err,
		)
	}

	return id
}

func clearBookingTestDatabase(
	ctx context.Context,
	pool *pgxpool.Pool,
) error {
	var databaseName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		return err
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return errors.New("refusing to truncate a database without the _test suffix")
	}
	_, err := pool.Exec(
		ctx,
		`
			TRUNCATE TABLE
				seat_assignments,
				flight_seats,
				booking_segments,
				booking_passengers,
				bookings,
				price_quote_items,
				price_quotes,
				fare_offers,
				flight_instances,
				flights,
				aircraft_seats,
				aircraft,
				aircraft_types,
				airports,
				users
			CASCADE
		`,
	)

	return err
}
