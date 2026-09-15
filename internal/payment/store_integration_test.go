package payment

import (
	"errors"
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestPaymentReplayAndExpiredHold(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{true: "expired", false: "confirmed"}[expired], func(t *testing.T) {
			ctx, pool := testsupport.Open(t)
			f := testsupport.Seed(t, ctx, pool)
			b := booking.NewStore(pool)
			_, err := b.HoldSeats(ctx, booking.HoldSeatsParams{UserID: f.User, BookingID: f.Booking, BookingSegmentID: f.Segment, FlightInstanceID: f.Flight, Seats: []booking.SeatSelection{{PassengerID: f.Passenger, FlightSeatID: f.Seat}}})
			if err != nil {
				t.Fatal(err)
			}
			fake := &FakeProvider{}
			s := Store{Pool: pool, Provider: fake}
			p, err := s.Start(ctx, f.User, f.Booking, "payment-test-key-0001")
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.Start(ctx, f.User, f.Booking, "payment-test-key-0001")
			if err != nil || again.ID != p.ID {
				t.Fatalf("idempotency: %v", err)
			}
			if _, err = s.Start(ctx, f.OtherUser, f.Booking, "payment-test-key-0001"); !errors.Is(err, booking.ErrBookingNotFound) {
				t.Fatal("ownership bypass")
			}
			if expired {
				if _, err = pool.Exec(ctx, `UPDATE bookings SET hold_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, f.Booking); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err = s.Dispatch(ctx, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			if fake.Calls != 1 {
				t.Fatal("double charge")
			}
			if err = s.Apply(ctx, "webhook", "duplicate-success", p.ID, Result{Reference: "replay", Status: "SUCCEEDED"}); err != nil {
				t.Fatal(err)
			}
			if err = s.Apply(ctx, "webhook", "late-failure", p.ID, Result{Reference: "replay", Status: "FAILED"}); err != nil {
				t.Fatal(err)
			}
			var state, pstate string
			if err = pool.QueryRow(ctx, `SELECT b.status,p.status FROM bookings b JOIN payments p ON p.booking_id=b.id WHERE b.id=$1`, f.Booking).Scan(&state, &pstate); err != nil {
				t.Fatal(err)
			}
			if expired {
				if state == "CONFIRMED" || pstate != "REFUND_PENDING" {
					t.Fatalf("late success: %s %s", state, pstate)
				}
			} else if state != "CONFIRMED" || pstate != "SUCCEEDED" {
				t.Fatalf("confirmation: %s %s", state, pstate)
			}
		})
	}
}

func TestRefundOrchestration(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE booking_segments SET refundable=true WHERE id=$1`, f.Segment); err != nil {
		t.Fatal(err)
	}
	b := booking.NewStore(pool)
	if _, err := b.HoldSeats(ctx, booking.HoldSeatsParams{UserID: f.User, BookingID: f.Booking, BookingSegmentID: f.Segment, FlightInstanceID: f.Flight, Seats: []booking.SeatSelection{{PassengerID: f.Passenger, FlightSeatID: f.Seat}}}); err != nil {
		t.Fatal(err)
	}
	fake := &FakeProvider{}
	s := Store{Pool: pool, Provider: fake}
	p, err := s.Start(ctx, f.User, f.Booking, "refund-payment-test-01")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	id, err := s.RequestRefund(ctx, f.User, f.Booking, "refund-request-test-01")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RequestRefund(ctx, f.User, f.Booking, "refund-request-test-01")
	if err != nil || again != id {
		t.Fatalf("refund replay: %v", err)
	}
	for range 2 {
		if err = s.DispatchRefund(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id=$1`, f.Booking).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "REFUNDED" || fake.Calls != 2 {
		t.Fatalf("state=%s provider operations=%d", state, fake.Calls)
	}
}
