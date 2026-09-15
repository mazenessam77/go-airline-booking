package booking

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func testPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := database.NewPostgresPool(ctx, dsn, 5, 1)
	if err != nil {
		t.Fatal("cannot connect to test database")
	}
	t.Cleanup(pool.Close)
	testsupport.Lock(t, pool)
	if err := clearBookingTestDatabase(ctx, pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := clearBookingTestDatabase(cleanup, pool); err != nil {
			t.Error(err)
		}
	})
	return ctx, pool
}

func (f seatRaceFixture) first() HoldSeatsParams {
	return HoldSeatsParams{UserID: f.UserOneID, BookingID: f.BookingOneID, BookingSegmentID: f.SegmentOneID,
		FlightInstanceID: f.FlightInstanceID, Seats: []SeatSelection{{PassengerID: f.PassengerOneID, FlightSeatID: f.FlightSeatID}}}
}

func (f seatRaceFixture) second() HoldSeatsParams {
	return HoldSeatsParams{UserID: f.UserTwoID, BookingID: f.BookingTwoID, BookingSegmentID: f.SegmentTwoID,
		FlightInstanceID: f.FlightInstanceID, Seats: []SeatSelection{{PassengerID: f.PassengerTwoID, FlightSeatID: f.FlightSeatID}}}
}

func execTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestHoldSeatFailures(t *testing.T) {
	for _, name := range []string{"wrong owner", "missing booking", "foreign passenger", "blocked", "booked", "inconsistent expiration"} {
		t.Run(name, func(t *testing.T) {
			ctx, pool := testPool(t)
			f := createSeatRaceFixture(t, ctx, pool)
			p := f.first()
			want := ErrSeatUnavailable
			switch name {
			case "wrong owner":
				p.UserID = f.UserTwoID
				want = ErrBookingNotFound
			case "missing booking":
				p.BookingID = f.UserOneID
				want = ErrBookingNotFound
			case "foreign passenger":
				p.Seats[0].PassengerID = f.PassengerTwoID
				want = ErrPassengerNotFound
			case "blocked":
				execTest(t, ctx, pool, `UPDATE flight_seats SET state='BLOCKED' WHERE id=$1`, f.FlightSeatID)
			case "booked", "inconsistent expiration":
				if _, err := NewStore(pool).HoldSeats(ctx, f.second()); err != nil {
					t.Fatal(err)
				}
				if name == "booked" {
					execTest(t, ctx, pool, `UPDATE seat_assignments SET status='CONFIRMED', hold_expires_at=NULL WHERE flight_seat_id=$1`, f.FlightSeatID)
					execTest(t, ctx, pool, `UPDATE flight_seats SET state='BOOKED', hold_expires_at=NULL WHERE id=$1`, f.FlightSeatID)
				} else {
					execTest(t, ctx, pool, `UPDATE flight_seats SET hold_expires_at=hold_expires_at + interval '1 second' WHERE id=$1`, f.FlightSeatID)
					want = ErrInventoryConflict
				}
			}
			if _, err := NewStore(pool).HoldSeats(ctx, p); !errors.Is(err, want) {
				t.Fatalf("want %v, got %v", want, err)
			}
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id=$1`, f.BookingOneID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != "DRAFT" {
				t.Fatalf("failed request changed booking to %s", status)
			}
		})
	}
}

func TestHoldRepeatedAndReclaimed(t *testing.T) {
	ctx, pool := testPool(t)
	f := createSeatRaceFixture(t, ctx, pool)
	s := NewStore(pool)
	first, err := s.HoldSeats(ctx, f.first())
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.HoldSeats(ctx, f.first())
	if err != nil {
		t.Fatal(err)
	}
	if !first.ExpiresAt.Equal(again.ExpiresAt) {
		t.Fatal("retry extended hold")
	}
	execTest(t, ctx, pool, `UPDATE seat_assignments SET created_at=clock_timestamp()-interval '1 hour', hold_expires_at=date_trunc('second',clock_timestamp())-interval '1 minute' WHERE booking_id=$1`, f.BookingOneID)
	execTest(t, ctx, pool, `UPDATE flight_seats SET hold_expires_at=(SELECT hold_expires_at FROM seat_assignments WHERE flight_seat_id=$1 AND status='HELD') WHERE id=$1`, f.FlightSeatID)
	execTest(t, ctx, pool, `UPDATE bookings SET hold_expires_at=(SELECT hold_expires_at FROM flight_seats WHERE id=$2) WHERE id=$1`, f.BookingOneID, f.FlightSeatID)
	if _, err := s.HoldSeats(ctx, f.first()); !errors.Is(err, ErrBookingNotHoldable) {
		t.Fatalf("expired owner: %v", err)
	}
	if _, err := s.HoldSeats(ctx, f.second()); err != nil {
		t.Fatal(err)
	}
	var active, expired int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='HELD'), count(*) FILTER (WHERE status='EXPIRED') FROM seat_assignments WHERE flight_seat_id=$1`, f.FlightSeatID).Scan(&active, &expired); err != nil {
		t.Fatal(err)
	}
	if active != 1 || expired != 1 {
		t.Fatalf("active=%d expired=%d", active, expired)
	}
}

func TestMultipleSeatsAtomic(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[unavailable], func(t *testing.T) {
			ctx, pool := testPool(t)
			f := createSeatRaceFixture(t, ctx, pool)
			seat := insertTestID(t, ctx, pool, `INSERT INTO aircraft_seats(aircraft_type_id,seat_number,row_number,seat_column,cabin) SELECT aircraft_type_id,'1B',1,'B','ECONOMY' FROM aircraft_seats LIMIT 1 RETURNING id::text`)
			secondSeat := insertTestID(t, ctx, pool, `INSERT INTO flight_seats(flight_instance_id,aircraft_seat_id) VALUES($1,$2) RETURNING id::text`, f.FlightInstanceID, seat)
			passenger := insertTestID(t, ctx, pool, `INSERT INTO booking_passengers(booking_id,passenger_type,first_name,last_name) VALUES($1,'ADULT','Extra','Passenger') RETURNING id::text`, f.BookingOneID)
			p := f.first()
			p.Seats = append(p.Seats, SeatSelection{PassengerID: passenger, FlightSeatID: secondSeat})
			if unavailable {
				// Block the last seat in lock order so an earlier insert must roll back.
				blocked := secondSeat
				if f.FlightSeatID > blocked {
					blocked = f.FlightSeatID
				}
				execTest(t, ctx, pool, `UPDATE flight_seats SET state='BLOCKED' WHERE id=$1`, blocked)
			}
			_, err := NewStore(pool).HoldSeats(ctx, p)
			if unavailable && !errors.Is(err, ErrSeatUnavailable) {
				t.Fatalf("got %v", err)
			}
			if !unavailable && err != nil {
				t.Fatal(err)
			}
			var assignments, held int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seat_assignments),(SELECT count(*) FROM flight_seats WHERE state='HELD')`).Scan(&assignments, &held); err != nil {
				t.Fatal(err)
			}
			want := 2
			if unavailable {
				want = 0
			}
			if assignments != want || held != want {
				t.Fatalf("assignments=%d held=%d want=%d", assignments, held, want)
			}
		})
	}
}
