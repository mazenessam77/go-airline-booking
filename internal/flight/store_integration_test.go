package flight

import (
	"testing"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestSearchAndSeatMap(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	s := Store{Pool: pool}
	items, err := s.Search(ctx, Search{Origin: "AAA", Destination: "BBB", Date: time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != f.Flight {
		t.Fatal("missing flight")
	}
	seats, err := s.Seats(ctx, f.Flight, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(seats) != 1 || seats[0].Availability != "AVAILABLE" {
		t.Fatal("seat availability")
	}
	if _, err = s.Seats(ctx, f.Flight, "", 1000); err != ErrInvalid {
		t.Fatal("unbounded seat query")
	}
	if _, err = pool.Exec(ctx, `UPDATE flight_seats SET state='HELD',booking_id=$2,hold_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, f.Seat, f.Booking); err != nil {
		t.Fatal(err)
	}
	seats, err = s.Seats(ctx, f.Flight, "", 10)
	if err != nil || seats[0].Availability != "UNAVAILABLE" {
		t.Fatalf("inconsistent inventory must fail closed: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE flight_instances SET status='CANCELLED' WHERE id=$1`, f.Flight); err != nil {
		t.Fatal(err)
	}
	fares, err := s.Fares(ctx, f.Flight)
	if err != nil || len(fares) != 0 {
		t.Fatalf("cancelled flight fare availability: %v", err)
	}
}
