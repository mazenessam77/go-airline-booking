package booking

import (
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func BenchmarkHoldSeatsExistingHold(b *testing.B) {
	ctx, pool := testsupport.Open(b)
	f := testsupport.Seed(b, ctx, pool)
	s := NewStore(pool)
	p := HoldSeatsParams{UserID: f.User, BookingID: f.Booking, BookingSegmentID: f.Segment, FlightInstanceID: f.Flight, Seats: []SeatSelection{{PassengerID: f.Passenger, FlightSeatID: f.Seat}}}
	if _, err := s.HoldSeats(ctx, p); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := s.HoldSeats(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
}
