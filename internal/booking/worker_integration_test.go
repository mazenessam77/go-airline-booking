package booking

import (
	"errors"
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestConcurrentExpirationWorkers(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	s := NewStore(pool)
	p := HoldSeatsParams{UserID: f.User, BookingID: f.Booking, BookingSegmentID: f.Segment, FlightInstanceID: f.Flight, Seats: []SeatSelection{{PassengerID: f.Passenger, FlightSeatID: f.Seat}}}
	if _, err := s.HoldSeats(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bookings SET hold_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, f.Booking); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := s.ExpireHolds(ctx, 10); results <- err }()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var state, assignment, seat string
	if err := pool.QueryRow(ctx, `SELECT b.status,sa.status,fs.state FROM bookings b JOIN seat_assignments sa ON sa.booking_id=b.id JOIN flight_seats fs ON fs.id=sa.flight_seat_id WHERE b.id=$1`, f.Booking).Scan(&state, &assignment, &seat); err != nil {
		t.Fatal(err)
	}
	if state != "EXPIRED" || assignment != "EXPIRED" || seat != "AVAILABLE" {
		t.Fatalf("%s %s %s", state, assignment, seat)
	}
	if n, err := s.ExpireHolds(ctx, 10); err != nil || n != 0 {
		t.Fatalf("worker replay: %d %v", n, err)
	}
}

func TestHoldIdempotency(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	s := NewStore(pool)
	p := HoldSeatsParams{UserID: f.User, BookingID: f.Booking, BookingSegmentID: f.Segment, FlightInstanceID: f.Flight, IdempotencyKey: "hold-idempotency-001", Seats: []SeatSelection{{PassengerID: f.Passenger, FlightSeatID: f.Seat}}}
	first, err := s.HoldSeats(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.HoldSeats(ctx, p)
	if err != nil || !first.ExpiresAt.Equal(again.ExpiresAt) {
		t.Fatalf("retry: %v", err)
	}
	p.Seats[0].FlightSeatID = f.Flight
	if _, err = s.HoldSeats(ctx, p); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("payload reuse: %v", err)
	}
}

func TestLifecycleTransitions(t *testing.T) {
	if CanTransition("EXPIRED", "CONFIRMED") || CanTransition("DRAFT", "TICKETED") || !CanTransition("PAYMENT_PENDING", "CONFIRMED") {
		t.Fatal("invalid lifecycle transitions")
	}
}
