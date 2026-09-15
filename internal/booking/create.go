package booking

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

type Summary struct {
	ID, PNR, Status, Currency string
	TotalMinor, Version       int64
	HoldExpiresAt             *time.Time
}
type Passenger struct {
	ID, Type, FirstName, LastName string
	DateOfBirth                   *time.Time
}
type Segment struct{ ID, FlightInstanceID, Status, Cabin string }
type Details struct {
	Summary        Summary
	PassengerCount int
	Passengers     []Passenger
	Segments       []Segment
}

func (s *Store) Create(ctx context.Context, user, quoteID, key string) (Summary, error) {
	if !validate.UUID(user) || !validate.UUID(quoteID) {
		return Summary{}, ErrInvalidHoldRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Summary{}, err
	}
	defer database.Rollback(tx)
	var result Summary
	record, replay, err := idempotency.Begin(ctx, tx, user, "POST /v1/bookings", key, quoteID, &result)
	if err != nil || replay {
		return result, err
	}
	var status string
	var live bool
	err = tx.QueryRow(ctx, `SELECT status,expires_at>clock_timestamp(),total_minor,currency FROM price_quotes WHERE id=$1 AND user_id=$2 FOR UPDATE`, quoteID, user).Scan(&status, &live, &result.TotalMinor, &result.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrBookingNotFound
	}
	if err != nil {
		return result, err
	}
	if status != "ACTIVE" || !live {
		return result, ErrBookingNotHoldable
	}
	// Fare and flight locks keep eligibility stable until quote consumption commits.
	locked, lockErr := tx.Query(ctx, `SELECT fo.id FROM price_quote_items qi JOIN fare_offers fo ON fo.id=qi.fare_offer_id
		JOIN flight_instances fi ON fi.id=qi.flight_instance_id WHERE qi.price_quote_id=$1 ORDER BY fo.id FOR SHARE OF fo,fi`, quoteID)
	if lockErr != nil {
		return result, lockErr
	}
	for locked.Next() {
	}
	lockErr = locked.Err()
	locked.Close()
	if lockErr != nil {
		return result, lockErr
	}
	if err = tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM price_quotes WHERE id=$1`, quoteID).Scan(&live); err != nil {
		return result, err
	}
	if !live {
		return result, ErrBookingNotHoldable
	}
	var invalid int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM price_quote_items qi JOIN fare_offers fo ON fo.id=qi.fare_offer_id JOIN flight_instances fi ON fi.id=qi.flight_instance_id
		WHERE qi.price_quote_id=$1 AND (NOT fo.is_active OR fo.valid_from>clock_timestamp() OR fo.valid_until<=clock_timestamp() OR fi.status NOT IN ('SCHEDULED','DELAYED') OR fi.scheduled_departure_at<=clock_timestamp())`, quoteID).Scan(&invalid); err != nil {
		return result, err
	}
	if invalid != 0 {
		return result, ErrBookingNotHoldable
	}
	for attempt := 0; attempt < 5; attempt++ {
		var raw [5]byte
		if _, err = rand.Read(raw[:]); err != nil {
			return result, err
		}
		result.PNR = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
		err = tx.QueryRow(ctx, `INSERT INTO bookings(user_id,price_quote_id,pnr,total_minor,currency) VALUES($1,$2,$3,$4,$5) ON CONFLICT(pnr) DO NOTHING RETURNING id::text,status,version`, user, quoteID, result.PNR, result.TotalMinor, result.Currency).Scan(&result.ID, &result.Status, &result.Version)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
	}
	if result.ID == "" {
		return result, ErrInventoryConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO booking_segments(booking_id,flight_instance_id,fare_offer_id,price_minor,taxes_minor,currency,refundable)
		SELECT $1,flight_instance_id,fare_offer_id,unit_price_minor*passenger_count,taxes_minor*passenger_count,currency,refundable FROM price_quote_items WHERE price_quote_id=$2`, result.ID, quoteID)
	if err != nil {
		return result, err
	}
	if tag.RowsAffected() < 1 || tag.RowsAffected() > 4 {
		return result, ErrBookingNotHoldable
	}
	if _, err = tx.Exec(ctx, `UPDATE price_quotes SET status='USED' WHERE id=$1`, quoteID); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) VALUES($1,'BOOKING_CREATED')`, result.ID); err != nil {
		return result, err
	}
	if err = record.Save(ctx, tx, result.ID, result); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) Get(ctx context.Context, user, id string) (Details, error) {
	if !validate.UUID(user) || !validate.UUID(id) {
		return Details{}, ErrInvalidHoldRequest
	}
	var d Details
	d.Passengers = []Passenger{}
	d.Segments = []Segment{}
	err := s.pool.QueryRow(ctx, `SELECT id::text,pnr,status,currency,total_minor,version,hold_expires_at,
		COALESCE((SELECT min(passenger_count) FROM price_quote_items WHERE price_quote_id=b.price_quote_id),0)
		FROM bookings b WHERE id=$1 AND user_id=$2`, id, user).Scan(&d.Summary.ID, &d.Summary.PNR, &d.Summary.Status, &d.Summary.Currency, &d.Summary.TotalMinor, &d.Summary.Version, &d.Summary.HoldExpiresAt, &d.PassengerCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrBookingNotFound
	}
	if err != nil {
		return d, err
	}
	rows, err := s.pool.Query(ctx, `SELECT p.id::text,p.passenger_type,p.first_name,p.last_name,p.date_of_birth FROM booking_passengers p JOIN bookings b ON b.id=p.booking_id WHERE b.id=$1 AND b.user_id=$2 ORDER BY p.id LIMIT 9`, id, user)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var p Passenger
		if err = rows.Scan(&p.ID, &p.Type, &p.FirstName, &p.LastName, &p.DateOfBirth); err != nil {
			rows.Close()
			return d, err
		}
		d.Passengers = append(d.Passengers, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = s.pool.Query(ctx, `SELECT bs.id::text,bs.flight_instance_id::text,bs.status,fo.cabin FROM booking_segments bs JOIN bookings b ON b.id=bs.booking_id JOIN fare_offers fo ON fo.id=bs.fare_offer_id WHERE b.id=$1 AND b.user_id=$2 ORDER BY bs.id LIMIT 4`, id, user)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Segment
		if err = rows.Scan(&v.ID, &v.FlightInstanceID, &v.Status, &v.Cabin); err != nil {
			return d, err
		}
		d.Segments = append(d.Segments, v)
	}
	return d, rows.Err()
}

func (s *Store) SavePassenger(ctx context.Context, user, bookingID, key string, p Passenger) (Passenger, error) {
	if !validate.UUID(user) || !validate.UUID(bookingID) || (p.ID != "" && !validate.UUID(p.ID)) || !validate.Name(p.FirstName) || !validate.Name(p.LastName) || (p.Type != "ADULT" && p.Type != "CHILD" && p.Type != "INFANT") {
		return Passenger{}, ErrInvalidHoldRequest
	}
	if p.DateOfBirth != nil && (p.DateOfBirth.After(time.Now()) || p.DateOfBirth.Before(time.Now().AddDate(-130, 0, 0))) {
		return Passenger{}, ErrInvalidHoldRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Passenger{}, err
	}
	defer database.Rollback(tx)
	var result Passenger
	record, replay, err := idempotency.Begin(ctx, tx, user, "PASSENGER "+bookingID+"/"+p.ID, key, p, &result)
	if err != nil || replay {
		return result, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM bookings WHERE id=$1 AND user_id=$2 FOR UPDATE`, bookingID, user).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrBookingNotFound
	}
	if err != nil {
		return result, err
	}
	if status != "DRAFT" {
		return result, ErrBookingNotHoldable
	}
	if p.ID == "" {
		var count, expected int
		err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM booking_passengers WHERE booking_id=$1),COALESCE((SELECT min(qi.passenger_count) FROM price_quote_items qi JOIN bookings b ON b.price_quote_id=qi.price_quote_id WHERE b.id=$1),0)`, bookingID).Scan(&count, &expected)
		if err != nil {
			return result, err
		}
		if count >= expected || count >= 9 {
			return result, ErrInvalidHoldRequest
		}
		err = tx.QueryRow(ctx, `INSERT INTO booking_passengers(booking_id,passenger_type,first_name,last_name,date_of_birth) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, bookingID, p.Type, p.FirstName, p.LastName, p.DateOfBirth).Scan(&p.ID)
	} else {
		var id string
		err = tx.QueryRow(ctx, `UPDATE booking_passengers SET passenger_type=$3,first_name=$4,last_name=$5,date_of_birth=$6 WHERE booking_id=$1 AND id=$2 RETURNING id::text`, bookingID, p.ID, p.Type, p.FirstName, p.LastName, p.DateOfBirth).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrBookingNotFound
	}
	if err != nil {
		return result, err
	}
	if err = record.Save(ctx, tx, p.ID, p); err != nil {
		return result, err
	}
	return p, tx.Commit(ctx)
}
