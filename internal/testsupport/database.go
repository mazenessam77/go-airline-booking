// Package testsupport contains helpers that refuse destructive work outside test databases.
package testsupport

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Lock(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("cannot acquire test lock connection")
	}
	var name string
	if err = conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil || !strings.HasSuffix(name, "_test") {
		conn.Release()
		t.Fatal("refusing non-test database")
	}
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(716402930)`); err != nil {
		conn.Release()
		t.Fatal("cannot acquire test isolation lock")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(716402930)`)
		conn.Release()
	})
}

func Open(t testing.TB) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	t.Cleanup(pool.Close)
	Lock(t, pool)
	clear := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE airports,aircraft_types,users,outbox_events,rate_limit_buckets CASCADE`); err != nil {
			t.Fatal("cannot clear isolated test database")
		}
	}
	clear()
	t.Cleanup(clear)
	return ctx, pool
}

type Fixture struct{ User, OtherUser, Booking, Quote, Segment, Passenger, Flight, Seat, Fare string }

func Seed(t testing.TB, ctx context.Context, pool *pgxpool.Pool) Fixture {
	t.Helper()
	var f Fixture
	err := pool.QueryRow(ctx, `WITH
	 u AS (INSERT INTO users(email,password_hash,first_name,last_name) VALUES('scenario@example.test','not-a-login-hash','Test','User') RETURNING id),
	 u2 AS (INSERT INTO users(email,password_hash,first_name,last_name) VALUES('other@example.test','not-a-login-hash','Other','User') RETURNING id),
	 a1 AS (INSERT INTO airports(iata_code,name,city,country_code,timezone) VALUES('AAA','Origin','Origin','US','UTC') RETURNING id),
	 a2 AS (INSERT INTO airports(iata_code,name,city,country_code,timezone) VALUES('BBB','Destination','Destination','US','UTC') RETURNING id),
	 at AS (INSERT INTO aircraft_types(manufacturer,model,code) VALUES('Test','Test','TEST') RETURNING id),
	 ac AS (INSERT INTO aircraft(aircraft_type_id,tail_number) SELECT id,'TEST-1' FROM at RETURNING id),
	 ast AS (INSERT INTO aircraft_seats(aircraft_type_id,seat_number,row_number,seat_column,cabin) SELECT id,'1A',1,'A','ECONOMY' FROM at RETURNING id),
	 fl AS (INSERT INTO flights(airline_code,flight_number,origin_airport_id,destination_airport_id) SELECT 'TT','100',a1.id,a2.id FROM a1,a2 RETURNING id),
	 fi AS (INSERT INTO flight_instances(flight_id,aircraft_id,scheduled_departure_at,scheduled_arrival_at) SELECT fl.id,ac.id,clock_timestamp()+interval '1 day',clock_timestamp()+interval '25 hours' FROM fl,ac RETURNING id),
	 fo AS (INSERT INTO fare_offers(flight_instance_id,fare_code,cabin,price_minor,currency) SELECT id,'ECO','ECONOMY',10000,'USD' FROM fi RETURNING id),
	 pq AS (INSERT INTO price_quotes(user_id,request_hash,status,total_minor,currency,expires_at) SELECT id,repeat('a',64),'USED',10000,'USD',clock_timestamp()+interval '10 minutes' FROM u RETURNING id),
	 qi AS (INSERT INTO price_quote_items(price_quote_id,flight_instance_id,fare_offer_id,passenger_count,unit_price_minor,total_minor,currency) SELECT pq.id,fi.id,fo.id,1,10000,10000,'USD' FROM pq,fi,fo),
	 b AS (INSERT INTO bookings(user_id,price_quote_id,pnr,total_minor,currency) SELECT u.id,pq.id,'SCEN01',10000,'USD' FROM u,pq RETURNING id),
	 bs AS (INSERT INTO booking_segments(booking_id,flight_instance_id,fare_offer_id,price_minor,currency) SELECT b.id,fi.id,fo.id,10000,'USD' FROM b,fi,fo RETURNING id),
	 bp AS (INSERT INTO booking_passengers(booking_id,passenger_type,first_name,last_name) SELECT id,'ADULT','Test','Passenger' FROM b RETURNING id),
	 fs AS (INSERT INTO flight_seats(flight_instance_id,aircraft_seat_id) SELECT fi.id,ast.id FROM fi,ast RETURNING id)
	 SELECT u.id::text,u2.id::text,b.id::text,pq.id::text,bs.id::text,bp.id::text,fi.id::text,fs.id::text,fo.id::text FROM u,u2,b,pq,bs,bp,fi,fs,fo`).Scan(&f.User, &f.OtherUser, &f.Booking, &f.Quote, &f.Segment, &f.Passenger, &f.Flight, &f.Seat, &f.Fare)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
