package demo

import (
	"testing"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestSeedIsSafeAndIdempotent(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	now := time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)

	first, err := Seed(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.FlightsAdded != 112 || first.SeatsAdded != 112*72 {
		t.Fatalf("unexpected first seed result: %+v", first)
	}
	second, err := Seed(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if second.FlightsAdded != 0 || second.SeatsAdded != 0 {
		t.Fatalf("seed is not idempotent: %+v", second)
	}

	var flights, blocked, booked int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM flight_instances`).Scan(&flights); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='BLOCKED'),count(*) FILTER (WHERE state='BOOKED') FROM flight_seats`).Scan(&blocked, &booked); err != nil {
		t.Fatal(err)
	}
	if flights != 112 || blocked != 224 || booked != 112 {
		t.Fatalf("unexpected inventory: flights=%d blocked=%d booked=%d", flights, blocked, booked)
	}
}
