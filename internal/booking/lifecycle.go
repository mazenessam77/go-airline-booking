package booking

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

func CanTransition(from, to string) bool {
	switch from {
	case "DRAFT":
		return to == "HELD" || to == "CANCELLED" || to == "EXPIRED"
	case "HELD":
		return to == "DRAFT" || to == "PAYMENT_PENDING" || to == "CANCELLED" || to == "EXPIRED"
	case "PAYMENT_PENDING":
		return to == "CONFIRMED" || to == "EXPIRED" || to == "CANCELLED"
	case "CONFIRMED":
		return to == "TICKETED" || to == "CANCELLED"
	case "TICKETED":
		return to == "CANCELLED"
	case "CANCELLED":
		return to == "REFUNDED"
	}
	return false
}

type AssignedSeat struct{ SeatID, PassengerID, SegmentID, Status string }

func (s *Store) Seats(ctx context.Context, user, id string) ([]AssignedSeat, error) {
	if _, err := s.Get(ctx, user, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT sa.flight_seat_id::text,sa.passenger_id::text,sa.booking_segment_id::text,
		CASE WHEN sa.status='HELD' AND sa.hold_expires_at<=clock_timestamp() THEN 'EXPIRED' ELSE sa.status END
		FROM seat_assignments sa JOIN bookings b ON b.id=sa.booking_id WHERE b.id=$1 AND b.user_id=$2
		AND sa.status IN ('HELD','CONFIRMED','CHECKED_IN') ORDER BY sa.flight_seat_id LIMIT 36`, id, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AssignedSeat{}
	for rows.Next() {
		var v AssignedSeat
		if err = rows.Scan(&v.SeatID, &v.PassengerID, &v.SegmentID, &v.Status); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) Release(ctx context.Context, user, id, key string, cancelBooking bool) error {
	if !validate.UUID(user) || !validate.UUID(id) {
		return ErrInvalidHoldRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var response struct{ Done bool }
	route := "DELETE /v1/bookings/" + id + "/seat-holds"
	if cancelBooking {
		route = "POST /v1/bookings/" + id + "/cancel"
	}
	record, replay, err := idempotency.Begin(ctx, tx, user, route, key, cancelBooking, &response)
	if err != nil || replay {
		return err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM bookings WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBookingNotFound
	}
	if err != nil {
		return err
	}
	if status != "DRAFT" && status != "HELD" {
		return ErrBookingNotHoldable
	}
	if err = releaseInventory(ctx, tx, id, "CANCELLED"); err != nil {
		return err
	}
	next, segment := "DRAFT", "DRAFT"
	if cancelBooking {
		next = "CANCELLED"
		segment = "CANCELLED"
	}
	if _, err = tx.Exec(ctx, `UPDATE booking_segments SET status=$2,updated_at=clock_timestamp() WHERE booking_id=$1`, id, segment); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bookings SET status=$2,hold_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, next); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,$2)`, id, "BOOKING_"+next); err != nil {
		return err
	}
	response.Done = true
	if err = record.Save(ctx, tx, id, response); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// All lifecycle writers lock booking, seats ordered by ID, then assignments.
func releaseInventory(ctx context.Context, tx pgx.Tx, id, status string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM flight_seats WHERE booking_id=$1 ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Reclaimed seats now belong to another booking and must never be released here.
	if _, err = tx.Exec(ctx, `UPDATE seat_assignments SET status=$2,updated_at=clock_timestamp() WHERE booking_id=$1 AND status='HELD'`, id, status); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE flight_seats SET state='AVAILABLE',booking_id=NULL,hold_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE booking_id=$1 AND state='HELD'`, id)
	return err
}

func (s *Store) ExpireHolds(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidHoldRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer database.Rollback(tx)
	rows, err := tx.Query(ctx, `SELECT id::text FROM bookings WHERE status IN ('HELD','PAYMENT_PENDING') AND hold_expires_at<=clock_timestamp() ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Lock inventory across the whole batch before updating any assignments.
	if len(ids) > 0 {
		locked, lockErr := tx.Query(ctx, `SELECT id FROM flight_seats WHERE booking_id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
		if lockErr != nil {
			return 0, lockErr
		}
		for locked.Next() {
		}
		lockErr = locked.Err()
		locked.Close()
		if lockErr != nil {
			return 0, lockErr
		}
	}
	for _, id := range ids {
		if err = releaseInventory(ctx, tx, id, "EXPIRED"); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE booking_segments SET status='CANCELLED',updated_at=clock_timestamp() WHERE booking_id=$1 AND status IN ('DRAFT','HELD')`, id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE bookings SET status='EXPIRED',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'BOOKING_EXPIRED')`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit(ctx)
}
