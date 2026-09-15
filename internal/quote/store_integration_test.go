package quote

import (
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestTrustedPricingAndSingleConsumption(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	s := Store{Pool: pool}
	if _, err := pool.Exec(ctx, `UPDATE fare_offers SET refundable=true WHERE id=$1`, f.Fare); err != nil {
		t.Fatal(err)
	}
	q, err := s.Create(ctx, f.User, []Selection{{FareOfferID: f.Fare, PassengerCount: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalMinor != 20000 || q.Currency != "USD" {
		t.Fatal("incorrect trusted pricing")
	}
	b := booking.NewStore(pool)
	if _, err := pool.Exec(ctx, `UPDATE fare_offers SET refundable=false WHERE id=$1`, f.Fare); err != nil {
		t.Fatal(err)
	}
	first, err := b.Create(ctx, f.User, q.ID, "booking-idempotency-001")
	if err != nil {
		t.Fatal(err)
	}
	var refundable bool
	if err = pool.QueryRow(ctx, `SELECT refundable FROM booking_segments WHERE booking_id=$1`, first.ID).Scan(&refundable); err != nil || !refundable {
		t.Fatalf("refund policy snapshot: %v", err)
	}
	again, err := b.Create(ctx, f.User, q.ID, "booking-idempotency-001")
	if err != nil || first.ID != again.ID {
		t.Fatalf("booking replay: %v", err)
	}
	if _, err = b.Create(ctx, f.User, q.ID, "booking-idempotency-002"); err == nil {
		t.Fatal("quote consumed twice")
	}
	if _, err = b.SavePassenger(ctx, f.User, first.ID, "passenger-test-0001", booking.Passenger{Type: "ADULT", FirstName: "Test", LastName: "Passenger"}); err != nil {
		t.Fatal(err)
	}
}
