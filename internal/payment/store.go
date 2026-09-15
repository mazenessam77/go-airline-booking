package payment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

type Store struct {
	Pool     *pgxpool.Pool
	Provider Provider
}
type Payment struct {
	ID, Status, Currency string
	AmountMinor          int64
}

func (s Store) Start(ctx context.Context, user, bookingID, key string) (Payment, error) {
	if !validate.UUID(user) || !validate.UUID(bookingID) {
		return Payment{}, booking.ErrInvalidHoldRequest
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Payment{}, err
	}
	defer database.Rollback(tx)
	var p Payment
	record, replay, err := idempotency.Begin(ctx, tx, user, "POST /v1/bookings/"+bookingID+"/payments", key, bookingID, &p)
	if err != nil || replay {
		return p, err
	}
	var state string
	var live bool
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(hold_expires_at>clock_timestamp(),false),total_minor,currency FROM bookings WHERE id=$1 AND user_id=$2 FOR UPDATE`, bookingID, user).Scan(&state, &live, &p.AmountMinor, &p.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, booking.ErrBookingNotFound
	}
	if err != nil {
		return p, err
	}
	if state == "PAYMENT_PENDING" {
		err = tx.QueryRow(ctx, `SELECT id::text,status,currency,amount_minor FROM payments WHERE booking_id=$1`, bookingID).Scan(&p.ID, &p.Status, &p.Currency, &p.AmountMinor)
		if err != nil {
			return p, err
		}
		if err = record.Save(ctx, tx, p.ID, p); err != nil {
			return p, err
		}
		return p, tx.Commit(ctx)
	}
	if state != "HELD" || !live {
		return p, booking.ErrBookingNotHoldable
	}
	var complete bool
	err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM booking_passengers WHERE booking_id=$1)>0 AND
		NOT EXISTS(SELECT 1 FROM booking_segments bs JOIN bookings b ON b.id=bs.booking_id
		JOIN price_quote_items qi ON qi.price_quote_id=b.price_quote_id AND qi.flight_instance_id=bs.flight_instance_id
		WHERE bs.booking_id=$1 AND (bs.status<>'HELD' OR qi.passenger_count<>(SELECT count(*) FROM booking_passengers WHERE booking_id=$1)
		OR qi.passenger_count<>(SELECT count(*) FROM seat_assignments sa WHERE sa.booking_segment_id=bs.id AND sa.status='HELD' AND sa.hold_expires_at>clock_timestamp())))`, bookingID).Scan(&complete)
	if err != nil {
		return p, err
	}
	if !complete {
		return p, booking.ErrBookingNotHoldable
	}
	err = tx.QueryRow(ctx, `INSERT INTO payments(booking_id,amount_minor,currency) VALUES($1,$2,$3) RETURNING id::text,status`, bookingID, p.AmountMinor, p.Currency).Scan(&p.ID, &p.Status)
	if err != nil {
		return p, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_attempts(payment_id) VALUES($1)`, p.ID); err != nil {
		return p, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bookings SET status='PAYMENT_PENDING',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, bookingID); err != nil {
		return p, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'PAYMENT_REQUESTED')`, p.ID); err != nil {
		return p, err
	}
	if err = record.Save(ctx, tx, p.ID, p); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

// Dispatch uses a stable provider key, including retries following ambiguous timeouts.
func (s Store) Dispatch(ctx context.Context, id string) error {
	if s.Provider == nil {
		return ErrUnavailable
	}
	if !validate.UUID(id) {
		return ErrInvalid
	}
	var req Request
	var status string
	req.PaymentID = id
	err := s.Pool.QueryRow(ctx, `SELECT pa.provider_key::text,p.amount_minor,p.currency,p.status FROM payments p JOIN payment_attempts pa ON pa.payment_id=p.id WHERE p.id=$1 ORDER BY pa.created_at LIMIT 1`, id).Scan(&req.Key, &req.AmountMinor, &req.Currency, &status)
	if err != nil {
		return err
	}
	if status == "SUCCEEDED" || status == "REFUND_PENDING" || status == "REFUNDED" {
		return nil
	}
	// No database transaction spans a provider call.
	result, err := s.Provider.Lookup(ctx, req.Key)
	if err != nil {
		result, err = s.Provider.Charge(ctx, req)
	}
	if err != nil {
		_, dbErr := s.Pool.Exec(ctx, `WITH changed AS (UPDATE payments SET status='RECONCILIATION_REQUIRED',version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND status IN ('PENDING','PROCESSING','RECONCILIATION_REQUIRED') RETURNING id) INSERT INTO outbox_events(aggregate_id,event_type) SELECT id,'PAYMENT_RECONCILIATION_REQUIRED' FROM changed`, id)
		if dbErr != nil {
			return dbErr
		}
		return ErrUnavailable
	}
	return s.Apply(ctx, "dispatch", req.Key+":"+result.Status, id, result)
}

// Apply accepts only authenticated provider events or server-side provider results.
func (s Store) Apply(ctx context.Context, provider, eventID, id string, result Result) error {
	if !validate.UUID(id) || len(eventID) < 1 || len(eventID) > 200 || len(provider) < 1 || len(provider) > 50 || len(result.Reference) > 200 || (result.Status != "SUCCEEDED" && result.Status != "FAILED") {
		return ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var bookingID, bookingStatus, paymentStatus string
	var live bool
	var amount int64
	err = tx.QueryRow(ctx, `SELECT b.id::text,b.status,COALESCE(b.hold_expires_at>clock_timestamp(),false) FROM bookings b JOIN payments p ON p.booking_id=b.id WHERE p.id=$1 FOR UPDATE OF b`, id).Scan(&bookingID, &bookingStatus, &live)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT status,amount_minor FROM payments WHERE id=$1 FOR UPDATE`, id).Scan(&paymentStatus, &amount)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO webhook_events(provider,event_id,payment_id,event_type) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, provider, eventID, id, result.Status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if paymentStatus == "SUCCEEDED" || paymentStatus == "REFUND_PENDING" || paymentStatus == "REFUNDED" {
		return tx.Commit(ctx)
	}
	if result.Status == "FAILED" {
		_, err = tx.Exec(ctx, `UPDATE payments SET status='FAILED',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE payment_attempts SET status='FAILED' WHERE payment_id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'PAYMENT_FAILED')`, id); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM flight_seats WHERE booking_id=$1 ORDER BY id FOR UPDATE`, bookingID)
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
	if err = tx.QueryRow(ctx, `SELECT COALESCE(hold_expires_at>clock_timestamp(),false) FROM bookings WHERE id=$1`, bookingID).Scan(&live); err != nil {
		return err
	}
	if bookingStatus != "PAYMENT_PENDING" || !live {
		if _, err = tx.Exec(ctx, `UPDATE payments SET status='REFUND_PENDING',provider_reference=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, result.Reference); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO refunds(payment_id,amount_minor) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, amount); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'REFUND_REQUESTED')`, id); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var assignments, seats int
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM seat_assignments WHERE booking_id=$1 AND status='HELD'),(SELECT count(*) FROM flight_seats WHERE booking_id=$1 AND state='HELD')`, bookingID).Scan(&assignments, &seats)
	if err != nil {
		return err
	}
	if seats == 0 || seats != assignments {
		return booking.ErrInventoryConflict
	}
	var inconsistent bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seat_assignments sa
		LEFT JOIN flight_seats fs ON fs.id=sa.flight_seat_id JOIN bookings b ON b.id=sa.booking_id
		WHERE sa.booking_id=$1 AND sa.status='HELD' AND (fs.booking_id IS DISTINCT FROM sa.booking_id
		OR fs.state<>'HELD' OR fs.hold_expires_at IS DISTINCT FROM sa.hold_expires_at
		OR sa.hold_expires_at IS DISTINCT FROM b.hold_expires_at))`, bookingID).Scan(&inconsistent)
	if err != nil {
		return err
	}
	if inconsistent {
		return booking.ErrInventoryConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE seat_assignments SET status='CONFIRMED',hold_expires_at=NULL,updated_at=clock_timestamp() WHERE booking_id=$1 AND status='HELD'`, bookingID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE flight_seats SET state='BOOKED',hold_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE booking_id=$1 AND state='HELD'`, bookingID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE booking_segments SET status='CONFIRMED',updated_at=clock_timestamp() WHERE booking_id=$1 AND status='HELD'`, bookingID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bookings SET status='CONFIRMED',hold_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, bookingID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE payments SET status='SUCCEEDED',provider_reference=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, result.Reference); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE payment_attempts SET status='SUCCEEDED' WHERE payment_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'BOOKING_CONFIRMED')`, bookingID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
