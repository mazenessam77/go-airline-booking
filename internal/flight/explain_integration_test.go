package flight

import (
	"os"
	"testing"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestSearchExplain(t *testing.T) {
	if os.Getenv("RUN_QUERY_PLANS") != "1" {
		t.Skip("set RUN_QUERY_PLANS=1 for the larger query-plan fixture")
	}
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	_, err := pool.Exec(ctx, `INSERT INTO flight_instances(flight_id,aircraft_id,scheduled_departure_at,scheduled_arrival_at)
		SELECT fi.flight_id,fi.aircraft_id,date_trunc('day',clock_timestamp())+n*interval '10 minutes',date_trunc('day',clock_timestamp())+n*interval '10 minutes'+interval '1 hour'
		FROM flight_instances fi CROSS JOIN generate_series(1,50000) n WHERE fi.id=$1 ON CONFLICT DO NOTHING`, f.Flight)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ANALYZE flight_instances`); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) SELECT fi.id,f.flight_number,fi.scheduled_departure_at
		FROM flight_instances fi JOIN flights f ON f.id=fi.flight_id JOIN airports o ON o.id=f.origin_airport_id JOIN airports d ON d.id=f.destination_airport_id
		WHERE o.iata_code=$1 AND d.iata_code=$2 AND fi.scheduled_departure_at>=$3
		AND fi.scheduled_departure_at<$4 AND fi.status IN ('SCHEDULED','DELAYED')
		AND fi.scheduled_departure_at>clock_timestamp() AND (fi.scheduled_departure_at,fi.id)>($5,$6::uuid)
		ORDER BY fi.scheduled_departure_at,fi.id LIMIT $7`, "AAA", "BBB", time.Now().UTC().Truncate(24*time.Hour).Add(24*time.Hour), time.Now().UTC().Truncate(24*time.Hour).Add(48*time.Hour), time.Time{}, "00000000-0000-0000-0000-000000000000", 20)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
