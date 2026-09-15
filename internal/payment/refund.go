package payment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

func (s Store) RequestRefund(ctx context.Context, user, bookingID, key string) (string, error) {
	if !validate.UUID(user) || !validate.UUID(bookingID) {
		return "", booking.ErrInvalidHoldRequest
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer database.Rollback(tx)
	var refundID string
	record, replay, err := idempotency.Begin(ctx, tx, user, "POST /v1/bookings/"+bookingID+"/refunds", key, bookingID, &refundID)
	if err != nil || replay {
		return refundID, err
	}
	var state, paymentID string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT status FROM bookings WHERE id=$1 AND user_id=$2 FOR UPDATE`, bookingID, user).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", booking.ErrBookingNotFound
	}
	if err != nil {
		return "", err
	}
	if state != "CONFIRMED" && state != "TICKETED" {
		return "", booking.ErrBookingNotHoldable
	}
	var refundable bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(bool_and(bs.refundable AND fi.scheduled_departure_at>clock_timestamp()),false) FROM booking_segments bs JOIN flight_instances fi ON fi.id=bs.flight_instance_id WHERE bs.booking_id=$1`, bookingID).Scan(&refundable)
	if err != nil {
		return "", err
	}
	if !refundable {
		return "", booking.ErrBookingNotHoldable
	}
	err = tx.QueryRow(ctx, `SELECT id::text,amount_minor FROM payments WHERE booking_id=$1 AND status='SUCCEEDED' FOR UPDATE`, bookingID).Scan(&paymentID, &amount)
	if err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM flight_seats WHERE booking_id=$1 ORDER BY id FOR UPDATE`, bookingID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE seat_assignments SET status='CANCELLED',updated_at=clock_timestamp() WHERE booking_id=$1 AND status IN ('CONFIRMED','CHECKED_IN')`, bookingID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE flight_seats SET state='AVAILABLE',booking_id=NULL,hold_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE booking_id=$1 AND state='BOOKED'`, bookingID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE booking_segments SET status='CANCELLED',updated_at=clock_timestamp() WHERE booking_id=$1`, bookingID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE bookings SET status='CANCELLED',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, bookingID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE payments SET status='REFUND_PENDING',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, paymentID); err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `INSERT INTO refunds(payment_id,amount_minor) VALUES($1,$2) RETURNING id::text`, paymentID, amount).Scan(&refundID)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'REFUND_REQUESTED')`, paymentID); err != nil {
		return "", err
	}
	if err = record.Save(ctx, tx, refundID, refundID); err != nil {
		return "", err
	}
	return refundID, tx.Commit(ctx)
}

func (s Store) DispatchRefund(ctx context.Context, paymentID string) error {
	if s.Provider == nil {
		return ErrUnavailable
	}
	if !validate.UUID(paymentID) {
		return ErrInvalid
	}
	var req Request
	var state string
	err := s.Pool.QueryRow(ctx, `SELECT r.id::text,r.amount_minor,p.currency,r.status FROM refunds r JOIN payments p ON p.id=r.payment_id WHERE p.id=$1`, paymentID).Scan(&req.Key, &req.AmountMinor, &req.Currency, &state)
	if err != nil {
		return err
	}
	if state == "SUCCEEDED" {
		return nil
	}
	req.PaymentID = paymentID
	result, err := s.Provider.Refund(ctx, req)
	if err != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE refunds SET status='UNKNOWN' WHERE payment_id=$1 AND status<>'SUCCEEDED'`, paymentID)
		return ErrUnavailable
	}
	if result.Status != "SUCCEEDED" {
		return ErrUnavailable
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var bookingID, bookingState string
	err = tx.QueryRow(ctx, `SELECT b.id::text,b.status FROM bookings b JOIN payments p ON p.booking_id=b.id WHERE p.id=$1 FOR UPDATE OF b`, paymentID).Scan(&bookingID, &bookingState)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE payments SET status='REFUNDED',version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND status='REFUND_PENDING'`, paymentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE refunds SET status='SUCCEEDED' WHERE payment_id=$1`, paymentID); err != nil {
		return err
	}
	if bookingState == "CANCELLED" {
		if _, err = tx.Exec(ctx, `UPDATE bookings SET status='REFUNDED',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, bookingID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'PAYMENT_REFUNDED')`, paymentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
