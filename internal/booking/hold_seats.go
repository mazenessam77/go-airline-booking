package booking

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
)

type lockedSeat struct {
	ID            string
	State         string
	BookingID     string
	HoldExpiresAt pgtype.Timestamptz
}

type lockedAssignment struct {
	ID               string
	FlightSeatID     string
	BookingID        string
	BookingSegmentID string
	PassengerID      string
	Status           string
	HoldExpiresAt    pgtype.Timestamptz
}

func (store *Store) HoldSeats(
	ctx context.Context,
	params HoldSeatsParams,
) (SeatHold, error) {
	normalizedParams, err := normalizeHoldParams(params)
	if err != nil {
		return SeatHold{}, err
	}

	tx, err := store.pool.BeginTx(
		ctx,
		pgx.TxOptions{
			IsoLevel: pgx.ReadCommitted,
		},
	)
	if err != nil {
		return SeatHold{}, fmt.Errorf(
			"begin seat hold transaction: %w",
			err,
		)
	}

	defer func() {
		rollbackContext, cancel := context.WithTimeout(
			context.Background(),
			2*time.Second,
		)
		defer cancel()

		_ = tx.Rollback(rollbackContext)
	}()

	var bookingStatus string
	var record idempotency.Record
	if normalizedParams.IdempotencyKey != "" {
		var previous SeatHold
		var replay bool
		record, replay, err = idempotency.Begin(ctx, tx, normalizedParams.UserID, "POST /v1/bookings/"+normalizedParams.BookingID+"/seat-holds", normalizedParams.IdempotencyKey, normalizedParams, &previous)
		if err != nil || replay {
			return previous, err
		}
	}
	var segmentStatus string
	var bookingHoldExpiresAt pgtype.Timestamptz

	err = tx.QueryRow(
		ctx,
		`
			SELECT
				b.status,
				b.hold_expires_at,
				bs.status
			FROM bookings AS b
			JOIN booking_segments AS bs
				ON bs.booking_id = b.id
			WHERE b.id = $1
			  AND bs.id = $2
			  AND bs.flight_instance_id = $3
			  AND b.user_id = $4
			FOR UPDATE OF b, bs
		`,
		normalizedParams.BookingID,
		normalizedParams.BookingSegmentID,
		normalizedParams.FlightInstanceID,
		normalizedParams.UserID,
	).Scan(
		&bookingStatus,
		&bookingHoldExpiresAt,
		&segmentStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SeatHold{}, ErrBookingNotFound
	}
	if err != nil {
		return SeatHold{}, fmt.Errorf(
			"lock booking and segment: %w",
			err,
		)
	}

	if segmentStatus != "DRAFT" &&
		segmentStatus != "HELD" {
		return SeatHold{}, ErrBookingNotHoldable
	}
	var departure time.Time
	var flightStatus string
	err = tx.QueryRow(ctx, `SELECT scheduled_departure_at,status FROM flight_instances WHERE id=$1 FOR SHARE`, normalizedParams.FlightInstanceID).Scan(&departure, &flightStatus)
	if err != nil {
		return SeatHold{}, err
	}
	if flightStatus != "SCHEDULED" && flightStatus != "DELAYED" {
		return SeatHold{}, ErrBookingNotHoldable
	}
	seatIDsToValidate := make([]string, 0, len(normalizedParams.Seats))
	for _, v := range normalizedParams.Seats {
		seatIDsToValidate = append(seatIDsToValidate, v.FlightSeatID)
	}
	var eligibleSeats int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM flight_seats fs
		JOIN aircraft_seats ast ON ast.id=fs.aircraft_seat_id JOIN flight_instances fi ON fi.id=fs.flight_instance_id
		JOIN aircraft ac ON ac.id=fi.aircraft_id JOIN booking_segments bs ON bs.flight_instance_id=fi.id
		JOIN fare_offers fo ON fo.id=bs.fare_offer_id
		WHERE bs.id=$1 AND fs.id=ANY($2::uuid[]) AND ast.cabin=fo.cabin AND ast.aircraft_type_id=ac.aircraft_type_id`, normalizedParams.BookingSegmentID, seatIDsToValidate).Scan(&eligibleSeats)
	if err != nil {
		return SeatHold{}, err
	}
	if eligibleSeats != len(normalizedParams.Seats) {
		return SeatHold{}, ErrSeatUnavailable
	}

	var databaseTime time.Time
	var newHoldExpiresAt time.Time

	holdSeconds := int64(
		store.seatHoldDuration / time.Second,
	)

	err = tx.QueryRow(
		ctx,
		`
			SELECT
				clock_timestamp(),
				clock_timestamp()
					+ ($1 * INTERVAL '1 second')
		`,
		holdSeconds,
	).Scan(
		&databaseTime,
		&newHoldExpiresAt,
	)
	if err != nil {
		return SeatHold{}, fmt.Errorf(
			"calculate seat hold expiration: %w",
			err,
		)
	}

	switch bookingStatus {
	case "DRAFT":
	case "HELD":
		if !bookingHoldExpiresAt.Valid ||
			!bookingHoldExpiresAt.Time.After(databaseTime) {
			return SeatHold{}, ErrBookingNotHoldable
		}

		newHoldExpiresAt =
			bookingHoldExpiresAt.Time

	default:
		return SeatHold{}, ErrBookingNotHoldable
	}

	err = lockPassengers(
		ctx,
		tx,
		normalizedParams.BookingID,
		normalizedParams.Seats,
	)
	if err != nil {
		return SeatHold{}, err
	}

	lockedSeats, err := lockFlightSeats(
		ctx,
		tx,
		normalizedParams.FlightInstanceID,
		normalizedParams.Seats,
	)
	if err != nil {
		return SeatHold{}, err
	}

	lockedAssignments, err := lockSeatAssignments(
		ctx,
		tx,
		normalizedParams.Seats,
	)
	if err != nil {
		return SeatHold{}, err
	}

	// Re-read wall time after lock waits; transaction timestamps can be stale.
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseTime); err != nil {
		return SeatHold{}, fmt.Errorf("read hold clock: %w", err)
	}
	if bookingStatus == "HELD" && !bookingHoldExpiresAt.Time.After(databaseTime) {
		return SeatHold{}, ErrBookingNotHoldable
	}
	if bookingStatus == "DRAFT" {
		newHoldExpiresAt = databaseTime.Add(store.seatHoldDuration)
	}
	if !departure.After(databaseTime) {
		return SeatHold{}, ErrBookingNotHoldable
	}
	if newHoldExpiresAt.After(departure) {
		newHoldExpiresAt = departure
	}

	err = applySeatAssignments(
		ctx,
		tx,
		normalizedParams,
		lockedSeats,
		lockedAssignments,
		databaseTime,
		newHoldExpiresAt,
	)
	if err != nil {
		return SeatHold{}, err
	}

	err = markFlightSeatsHeld(
		ctx,
		tx,
		normalizedParams,
		newHoldExpiresAt,
	)
	if err != nil {
		return SeatHold{}, err
	}

	commandTag, err := tx.Exec(
		ctx,
		`
			UPDATE booking_segments
			SET
				status = 'HELD',
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
			  AND booking_id = $2
		`,
		normalizedParams.BookingSegmentID,
		normalizedParams.BookingID,
	)
	if err != nil {
		return SeatHold{}, fmt.Errorf(
			"update booking segment: %w",
			err,
		)
	}
	if commandTag.RowsAffected() != 1 {
		return SeatHold{}, ErrInventoryConflict
	}

	commandTag, err = tx.Exec(
		ctx,
		`
			UPDATE bookings
			SET
				status = 'HELD',
				hold_expires_at = $2,
				version = version + 1,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
		`,
		normalizedParams.BookingID,
		newHoldExpiresAt,
	)
	if err != nil {
		return SeatHold{}, fmt.Errorf(
			"update booking hold: %w",
			err,
		)
	}
	if commandTag.RowsAffected() != 1 {
		return SeatHold{}, ErrInventoryConflict
	}

	seatIDs := make(
		[]string,
		0,
		len(normalizedParams.Seats),
	)

	for _, selection := range normalizedParams.Seats {
		seatIDs = append(
			seatIDs,
			selection.FlightSeatID,
		)
	}

	result := SeatHold{
		BookingID: normalizedParams.BookingID,
		SeatIDs:   seatIDs,
		ExpiresAt: newHoldExpiresAt,
	}
	if normalizedParams.IdempotencyKey != "" {
		if err = record.Save(ctx, tx, result.BookingID, result); err != nil {
			return SeatHold{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'SEATS_HELD')`, result.BookingID); err != nil {
		return SeatHold{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SeatHold{}, fmt.Errorf("commit seat hold transaction: %w", err)
	}
	return result, nil
}

func normalizeHoldParams(
	params HoldSeatsParams,
) (HoldSeatsParams, error) {
	if len(params.Seats) < 1 ||
		len(params.Seats) > 9 {
		return HoldSeatsParams{}, ErrInvalidHoldRequest
	}

	normalized := params
	normalized.Seats = make(
		[]SeatSelection,
		0,
		len(params.Seats),
	)

	var valid bool

	normalized.UserID, valid = normalizeUUID(params.UserID)
	if !valid {
		return HoldSeatsParams{}, ErrInvalidHoldRequest
	}

	normalized.BookingID, valid =
		normalizeUUID(params.BookingID)
	if !valid {
		return HoldSeatsParams{}, ErrInvalidHoldRequest
	}

	normalized.BookingSegmentID, valid =
		normalizeUUID(params.BookingSegmentID)
	if !valid {
		return HoldSeatsParams{}, ErrInvalidHoldRequest
	}

	normalized.FlightInstanceID, valid =
		normalizeUUID(params.FlightInstanceID)
	if !valid {
		return HoldSeatsParams{}, ErrInvalidHoldRequest
	}

	passengerIDs := make(map[string]struct{})
	seatIDs := make(map[string]struct{})

	for _, selection := range params.Seats {
		passengerID, passengerValid :=
			normalizeUUID(selection.PassengerID)

		flightSeatID, seatValid :=
			normalizeUUID(selection.FlightSeatID)

		if !passengerValid || !seatValid {
			return HoldSeatsParams{}, ErrInvalidHoldRequest
		}

		if _, exists := passengerIDs[passengerID]; exists {
			return HoldSeatsParams{}, ErrInvalidHoldRequest
		}

		if _, exists := seatIDs[flightSeatID]; exists {
			return HoldSeatsParams{}, ErrInvalidHoldRequest
		}

		passengerIDs[passengerID] = struct{}{}
		seatIDs[flightSeatID] = struct{}{}

		normalized.Seats = append(
			normalized.Seats,
			SeatSelection{
				PassengerID:  passengerID,
				FlightSeatID: flightSeatID,
			},
		)
	}

	sort.Slice(
		normalized.Seats,
		func(first int, second int) bool {
			return normalized.Seats[first].FlightSeatID <
				normalized.Seats[second].FlightSeatID
		},
	)

	return normalized, nil
}

func normalizeUUID(value string) (string, bool) {
	normalized := strings.ToLower(
		strings.TrimSpace(value),
	)

	if len(normalized) != 36 || normalized[8] != '-' || normalized[13] != '-' || normalized[18] != '-' || normalized[23] != '-' {
		return "", false
	}

	var uuid pgtype.UUID

	if err := uuid.Scan(normalized); err != nil {
		return "", false
	}

	return normalized, uuid.Valid && uuid.Bytes != [16]byte{}
}

func lockPassengers(
	ctx context.Context,
	tx pgx.Tx,
	bookingID string,
	selections []SeatSelection,
) error {
	passengerIDs := make(
		[]string,
		0,
		len(selections),
	)

	for _, selection := range selections {
		passengerIDs = append(
			passengerIDs,
			selection.PassengerID,
		)
	}

	sort.Strings(passengerIDs)

	query := `
		SELECT id::text
		FROM booking_passengers
		WHERE booking_id = $1
		  AND id IN (` +
		placeholders(2, len(passengerIDs)) +
		`)
		ORDER BY id
		FOR KEY SHARE
	`

	arguments := make(
		[]any,
		0,
		len(passengerIDs)+1,
	)

	arguments = append(arguments, bookingID)

	for _, passengerID := range passengerIDs {
		arguments = append(arguments, passengerID)
	}

	rows, err := tx.Query(
		ctx,
		query,
		arguments...,
	)
	if err != nil {
		return fmt.Errorf(
			"lock booking passengers: %w",
			err,
		)
	}

	found := 0

	for rows.Next() {
		var passengerID string

		if err := rows.Scan(&passengerID); err != nil {
			rows.Close()

			return fmt.Errorf(
				"scan booking passenger: %w",
				err,
			)
		}

		found++
	}

	rowsError := rows.Err()
	rows.Close()

	if rowsError != nil {
		return fmt.Errorf(
			"read booking passengers: %w",
			rowsError,
		)
	}

	if found != len(passengerIDs) {
		return ErrPassengerNotFound
	}

	return nil
}

func lockFlightSeats(
	ctx context.Context,
	tx pgx.Tx,
	flightInstanceID string,
	selections []SeatSelection,
) (map[string]lockedSeat, error) {
	query := `
		SELECT
			id::text,
			state,
			COALESCE(booking_id::text, ''),
			hold_expires_at
		FROM flight_seats
		WHERE flight_instance_id = $1
		  AND id IN (` +
		placeholders(2, len(selections)) +
		`)
		ORDER BY id
		FOR UPDATE
	`

	arguments := make(
		[]any,
		0,
		len(selections)+1,
	)

	arguments = append(
		arguments,
		flightInstanceID,
	)

	for _, selection := range selections {
		arguments = append(
			arguments,
			selection.FlightSeatID,
		)
	}

	rows, err := tx.Query(
		ctx,
		query,
		arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"lock flight seats: %w",
			err,
		)
	}

	seats := make(
		map[string]lockedSeat,
		len(selections),
	)

	for rows.Next() {
		var seat lockedSeat

		err := rows.Scan(
			&seat.ID,
			&seat.State,
			&seat.BookingID,
			&seat.HoldExpiresAt,
		)
		if err != nil {
			rows.Close()

			return nil, fmt.Errorf(
				"scan flight seat: %w",
				err,
			)
		}

		seats[seat.ID] = seat
	}

	rowsError := rows.Err()
	rows.Close()

	if rowsError != nil {
		return nil, fmt.Errorf(
			"read flight seats: %w",
			rowsError,
		)
	}

	if len(seats) != len(selections) {
		return nil, ErrSeatUnavailable
	}

	return seats, nil
}

func lockSeatAssignments(
	ctx context.Context,
	tx pgx.Tx,
	selections []SeatSelection,
) (map[string]lockedAssignment, error) {
	query := `
		SELECT
			id::text,
			flight_seat_id::text,
			booking_id::text,
			booking_segment_id::text,
			passenger_id::text,
			status,
			hold_expires_at
		FROM seat_assignments
		WHERE flight_seat_id IN (` +
		placeholders(1, len(selections)) +
		`)
		  AND status IN (
			'HELD',
			'CONFIRMED',
			'CHECKED_IN'
		  )
		ORDER BY flight_seat_id
		FOR UPDATE
	`

	arguments := make(
		[]any,
		0,
		len(selections),
	)

	for _, selection := range selections {
		arguments = append(
			arguments,
			selection.FlightSeatID,
		)
	}

	rows, err := tx.Query(
		ctx,
		query,
		arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"lock seat assignments: %w",
			err,
		)
	}

	assignments := make(
		map[string]lockedAssignment,
		len(selections),
	)

	for rows.Next() {
		var assignment lockedAssignment

		err := rows.Scan(
			&assignment.ID,
			&assignment.FlightSeatID,
			&assignment.BookingID,
			&assignment.BookingSegmentID,
			&assignment.PassengerID,
			&assignment.Status,
			&assignment.HoldExpiresAt,
		)
		if err != nil {
			rows.Close()

			return nil, fmt.Errorf(
				"scan seat assignment: %w",
				err,
			)
		}

		if _, exists :=
			assignments[assignment.FlightSeatID]; exists {
			rows.Close()

			return nil, ErrInventoryConflict
		}

		assignments[assignment.FlightSeatID] =
			assignment
	}

	rowsError := rows.Err()
	rows.Close()

	if rowsError != nil {
		return nil, fmt.Errorf(
			"read seat assignments: %w",
			rowsError,
		)
	}

	return assignments, nil
}

func applySeatAssignments(
	ctx context.Context,
	tx pgx.Tx,
	params HoldSeatsParams,
	seats map[string]lockedSeat,
	assignments map[string]lockedAssignment,
	databaseTime time.Time,
	holdExpiresAt time.Time,
) error {
	for _, selection := range params.Seats {
		seat := seats[selection.FlightSeatID]

		assignment, hasAssignment :=
			assignments[selection.FlightSeatID]

		switch seat.State {
		case "AVAILABLE":
			if hasAssignment {
				return ErrInventoryConflict
			}

		case "HELD":
			if !seat.HoldExpiresAt.Valid ||
				!hasAssignment ||
				assignment.Status != "HELD" ||
				!assignment.HoldExpiresAt.Valid {
				return ErrInventoryConflict
			}
			if seat.BookingID != assignment.BookingID ||
				!seat.HoldExpiresAt.Time.Equal(assignment.HoldExpiresAt.Time) {
				return ErrInventoryConflict
			}

			seatExpired :=
				!seat.HoldExpiresAt.Time.After(databaseTime)

			assignmentExpired :=
				!assignment.HoldExpiresAt.Time.After(databaseTime)

			if seatExpired != assignmentExpired {
				return ErrInventoryConflict
			}

			if seatExpired {
				commandTag, err := tx.Exec(
					ctx,
					`
						UPDATE seat_assignments
						SET
							status = 'EXPIRED',
							updated_at = CURRENT_TIMESTAMP
						WHERE id = $1
						  AND status = 'HELD'
					`,
					assignment.ID,
				)
				if err != nil {
					return fmt.Errorf(
						"expire old seat assignment: %w",
						err,
					)
				}
				if commandTag.RowsAffected() != 1 {
					return ErrInventoryConflict
				}
			} else {
				if seat.BookingID != params.BookingID ||
					assignment.BookingID != params.BookingID ||
					assignment.BookingSegmentID !=
						params.BookingSegmentID ||
					assignment.PassengerID !=
						selection.PassengerID {
					return fmt.Errorf(
						"%w: seat %s",
						ErrSeatUnavailable,
						selection.FlightSeatID,
					)
				}

				if !seat.HoldExpiresAt.Time.Equal(
					holdExpiresAt,
				) ||
					!assignment.HoldExpiresAt.Time.Equal(
						holdExpiresAt,
					) {
					return ErrInventoryConflict
				}

				continue
			}

		case "BOOKED":
			assignmentIsBooked := hasAssignment &&
				(assignment.Status == "CONFIRMED" ||
					assignment.Status == "CHECKED_IN")

			if !assignmentIsBooked {
				return ErrInventoryConflict
			}

			return fmt.Errorf(
				"%w: seat %s",
				ErrSeatUnavailable,
				selection.FlightSeatID,
			)

		case "BLOCKED":
			if hasAssignment {
				return ErrInventoryConflict
			}

			return fmt.Errorf(
				"%w: seat %s",
				ErrSeatUnavailable,
				selection.FlightSeatID,
			)

		default:
			return ErrInventoryConflict
		}

		_, err := tx.Exec(
			ctx,
			`
				INSERT INTO seat_assignments (
					booking_id,
					booking_segment_id,
					flight_instance_id,
					passenger_id,
					flight_seat_id,
					status,
					hold_expires_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					'HELD',
					$6
				)
			`,
			params.BookingID,
			params.BookingSegmentID,
			params.FlightInstanceID,
			selection.PassengerID,
			selection.FlightSeatID,
			holdExpiresAt,
		)
		if err != nil {
			return mapSeatWriteError(err)
		}
	}

	return nil
}

func markFlightSeatsHeld(
	ctx context.Context,
	tx pgx.Tx,
	params HoldSeatsParams,
	holdExpiresAt time.Time,
) error {
	query := `
		UPDATE flight_seats
		SET
			booking_id = $1,
			state = 'HELD',
			hold_expires_at = $2,
			version = version + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE flight_instance_id = $3
		  AND id IN (` +
		placeholders(4, len(params.Seats)) +
		`)
	`

	arguments := make(
		[]any,
		0,
		len(params.Seats)+3,
	)

	arguments = append(
		arguments,
		params.BookingID,
		holdExpiresAt,
		params.FlightInstanceID,
	)

	for _, selection := range params.Seats {
		arguments = append(
			arguments,
			selection.FlightSeatID,
		)
	}

	commandTag, err := tx.Exec(
		ctx,
		query,
		arguments...,
	)
	if err != nil {
		return fmt.Errorf(
			"mark flight seats held: %w",
			err,
		)
	}

	if commandTag.RowsAffected() !=
		int64(len(params.Seats)) {
		return ErrInventoryConflict
	}

	return nil
}

func placeholders(
	start int,
	count int,
) string {
	values := make([]string, count)

	for index := 0; index < count; index++ {
		values[index] = fmt.Sprintf(
			"$%d",
			start+index,
		)
	}

	return strings.Join(values, ", ")
}

func mapSeatWriteError(err error) error {
	var postgresError *pgconn.PgError

	if errors.As(err, &postgresError) &&
		postgresError.Code == "23505" {
		return ErrSeatUnavailable
	}

	return fmt.Errorf(
		"write seat assignment: %w",
		err,
	)
}
