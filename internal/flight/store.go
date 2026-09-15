package flight

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

var ErrInvalid = errors.New("invalid flight query")
var ErrNotFound = errors.New("flight not found")

type Store struct{ Pool *pgxpool.Pool }
type Search struct {
	Origin, Destination, Date, AfterID string
	AfterDeparture                     time.Time
	Limit                              int
}
type Flight struct {
	ID, Airline, Number, Origin, Destination, Status string
	Departure, Arrival                               time.Time
}
type Seat struct{ ID, Number, Cabin, Availability string }

type Fare struct {
	ID, Code, Cabin, Currency string
	PriceMinor, TaxesMinor    int64
	Refundable                bool
}

func (s Store) Fares(ctx context.Context, id string) ([]Fare, error) {
	if !validate.UUID(id) {
		return nil, ErrInvalid
	}
	rows, err := s.Pool.Query(ctx, `SELECT fo.id::text,fo.fare_code,fo.cabin,fo.currency,fo.price_minor,fo.taxes_minor,fo.refundable FROM fare_offers fo
		JOIN flight_instances fi ON fi.id=fo.flight_instance_id
		WHERE fo.flight_instance_id=$1 AND fo.is_active AND fo.valid_from<=clock_timestamp() AND (fo.valid_until IS NULL OR fo.valid_until>clock_timestamp())
		AND fi.status IN ('SCHEDULED','DELAYED') AND fi.scheduled_departure_at>clock_timestamp() ORDER BY fo.id LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Fare{}
	for rows.Next() {
		var f Fare
		if err = rows.Scan(&f.ID, &f.Code, &f.Cabin, &f.Currency, &f.PriceMinor, &f.TaxesMinor, &f.Refundable); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

func (s Store) Search(ctx context.Context, q Search) ([]Flight, error) {
	date, err := time.Parse("2006-01-02", q.Date)
	if err != nil || !validate.Airport(q.Origin) || !validate.Airport(q.Destination) || q.Origin == q.Destination || q.Limit < 1 || q.Limit > 100 || (q.AfterID != "" && (!validate.UUID(q.AfterID) || q.AfterDeparture.IsZero())) {
		return nil, ErrInvalid
	}
	after := q.AfterID
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := s.Pool.Query(ctx, `SELECT fi.id::text,f.airline_code,f.flight_number,o.iata_code,d.iata_code,fi.status,fi.scheduled_departure_at,fi.scheduled_arrival_at
		FROM flight_instances fi JOIN flights f ON f.id=fi.flight_id JOIN airports o ON o.id=f.origin_airport_id JOIN airports d ON d.id=f.destination_airport_id
		WHERE o.iata_code=$1 AND d.iata_code=$2 AND fi.scheduled_departure_at >= $3 AND fi.scheduled_departure_at < $4
		AND fi.status IN ('SCHEDULED','DELAYED') AND fi.scheduled_departure_at>clock_timestamp()
		AND (fi.scheduled_departure_at,fi.id)>($5,$6::uuid)
		ORDER BY fi.scheduled_departure_at,fi.id LIMIT $7`, q.Origin, q.Destination, date, date.AddDate(0, 0, 1), q.AfterDeparture, after, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Flight, 0, q.Limit)
	for rows.Next() {
		var f Flight
		if err = rows.Scan(&f.ID, &f.Airline, &f.Number, &f.Origin, &f.Destination, &f.Status, &f.Departure, &f.Arrival); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

func (s Store) Get(ctx context.Context, id string) (Flight, error) {
	if !validate.UUID(id) {
		return Flight{}, ErrInvalid
	}
	var f Flight
	err := s.Pool.QueryRow(ctx, `SELECT fi.id::text,f.airline_code,f.flight_number,o.iata_code,d.iata_code,fi.status,fi.scheduled_departure_at,fi.scheduled_arrival_at
		FROM flight_instances fi JOIN flights f ON f.id=fi.flight_id JOIN airports o ON o.id=f.origin_airport_id JOIN airports d ON d.id=f.destination_airport_id WHERE fi.id=$1`, id).Scan(&f.ID, &f.Airline, &f.Number, &f.Origin, &f.Destination, &f.Status, &f.Departure, &f.Arrival)
	if errors.Is(err, pgx.ErrNoRows) {
		return Flight{}, ErrNotFound
	}
	return f, err
}

func (s Store) Seats(ctx context.Context, id, after string, limit int) ([]Seat, error) {
	if !validate.UUID(id) || limit < 1 || limit > 100 || (after != "" && !validate.UUID(after)) {
		return nil, ErrInvalid
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := s.Pool.Query(ctx, `SELECT fs.id::text,a.seat_number,a.cabin,
		CASE WHEN fi.status IN ('SCHEDULED','DELAYED') AND fi.scheduled_departure_at>clock_timestamp() AND
		((fs.state='AVAILABLE' AND NOT EXISTS(SELECT 1 FROM seat_assignments sa WHERE sa.flight_seat_id=fs.id AND sa.status IN ('HELD','CONFIRMED','CHECKED_IN')))
		OR (fs.state='HELD' AND fs.hold_expires_at<=clock_timestamp() AND EXISTS(SELECT 1 FROM seat_assignments sa
		WHERE sa.flight_seat_id=fs.id AND sa.status='HELD' AND sa.booking_id=fs.booking_id AND sa.hold_expires_at=fs.hold_expires_at)))
		THEN 'AVAILABLE' ELSE 'UNAVAILABLE' END
		FROM flight_seats fs JOIN aircraft_seats a ON a.id=fs.aircraft_seat_id JOIN flight_instances fi ON fi.id=fs.flight_instance_id
		WHERE fs.flight_instance_id=$1 AND fs.id>$2::uuid ORDER BY fs.id LIMIT $3`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Seat, 0, limit)
	for rows.Next() {
		var seat Seat
		if err = rows.Scan(&seat.ID, &seat.Number, &seat.Cabin, &seat.Availability); err != nil {
			return nil, err
		}
		result = append(result, seat)
	}
	return result, rows.Err()
}
