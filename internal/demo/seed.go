// Package demo creates fictional, isolated development fixtures without resetting user data.
package demo

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/auth"
	"github.com/mazenessam77/go-airline-booking/internal/database"
)

type Result struct{ FlightsAdded, SeatsAdded int }

// Seed only accepts the dedicated demo database or an isolated test database.
// Existing departures, prices, bookings, and inventory are never reset.
func Seed(ctx context.Context, pool *pgxpool.Pool, now time.Time) (Result, error) {
	var result Result
	var name string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return result, err
	}
	if name != "airline_booking_demo" && !strings.HasSuffix(name, "_test") {
		return result, errors.New("demo seeding requires airline_booking_demo or an isolated _test database")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(716402931)`); err != nil {
		return result, err
	}
	airports := []struct{ code, name, city, country, zone string }{
		{"CAI", "Cairo International", "Cairo", "EG", "Africa/Cairo"},
		{"DXB", "Dubai International", "Dubai", "AE", "Asia/Dubai"},
		{"LHR", "London Heathrow", "London", "GB", "Europe/London"},
		{"JED", "King Abdulaziz International", "Jeddah", "SA", "Asia/Riyadh"},
		{"ASW", "Aswan International", "Aswan", "EG", "Africa/Cairo"},
	}
	ids := map[string]string{}
	for _, a := range airports {
		var id string
		err = tx.QueryRow(ctx, `INSERT INTO airports(iata_code,name,city,country_code,timezone) VALUES($1,$2,$3,$4,$5) ON CONFLICT(iata_code) DO UPDATE SET iata_code=excluded.iata_code RETURNING id::text`, a.code, a.name, a.city, a.country, a.zone).Scan(&id)
		if err != nil {
			return result, err
		}
		ids[a.code] = id
	}
	var aircraftType string
	err = tx.QueryRow(ctx, `INSERT INTO aircraft_types(manufacturer,model,code) VALUES('Demo','72-seat demonstration cabin','DEMO-A320') ON CONFLICT(code) DO UPDATE SET code=excluded.code RETURNING id::text`).Scan(&aircraftType)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO aircraft_seats(aircraft_type_id,seat_number,row_number,seat_column,cabin,is_window,is_aisle)
		SELECT $1,r::text||c,r,c,CASE WHEN r<=2 THEN 'BUSINESS' ELSE 'ECONOMY' END,c IN ('A','F'),c IN ('C','D')
		FROM generate_series(1,12) r CROSS JOIN unnest(ARRAY['A','B','C','D','E','F']) c ON CONFLICT(aircraft_type_id,seat_number) DO NOTHING`, aircraftType)
	if err != nil {
		return result, err
	}
	// This suspended fixture owner has a random, discarded password, not a demo login.
	hash, err := auth.HashPassword(rand.Text())
	if err != nil {
		return result, err
	}
	var owner string
	err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash,first_name,last_name,status) VALUES('inventory-fixture@example.test',$1,'Fictional','Inventory','SUSPENDED') ON CONFLICT(LOWER(email)) DO UPDATE SET email=excluded.email RETURNING id::text`, hash).Scan(&owner)
	if err != nil {
		return result, err
	}
	base := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	routes := []struct {
		destination         string
		minutes, price, tax int
	}{{"DXB", 210, 620000, 48000}, {"LHR", 310, 1390000, 85000}, {"JED", 135, 450000, 39000}, {"ASW", 85, 210000, 18000}}
	for routeIndex, route := range routes {
		for direction := 0; direction < 2; direction++ {
			for departureIndex, hour := range []int{8, 17} {
				origin, destination := "CAI", route.destination
				if direction == 1 {
					origin, destination = destination, origin
				}
				// Each service has a dedicated fictional aircraft, avoiding overlapping rotations.
				tail := fmt.Sprintf("DEMO-%02d-%d-%d", routeIndex+1, direction, departureIndex)
				var aircraft, flight string
				err = tx.QueryRow(ctx, `INSERT INTO aircraft(aircraft_type_id,tail_number) VALUES($1,$2) ON CONFLICT(tail_number) DO UPDATE SET tail_number=excluded.tail_number RETURNING id::text`, aircraftType, tail).Scan(&aircraft)
				if err != nil {
					return result, err
				}
				number := fmt.Sprintf("%d", 101+routeIndex*4+direction*2+departureIndex)
				err = tx.QueryRow(ctx, `INSERT INTO flights(airline_code,flight_number,origin_airport_id,destination_airport_id) VALUES('DM',$1,$2,$3) ON CONFLICT(airline_code,flight_number,origin_airport_id,destination_airport_id) DO UPDATE SET flight_number=excluded.flight_number RETURNING id::text`, number, ids[origin], ids[destination]).Scan(&flight)
				if err != nil {
					return result, err
				}
				for offset := 1; offset <= 7; offset++ {
					departure := base.AddDate(0, 0, offset).Add(time.Duration(hour) * time.Hour)
					var instance string
					err = tx.QueryRow(ctx, `INSERT INTO flight_instances(flight_id,aircraft_id,scheduled_departure_at,scheduled_arrival_at) VALUES($1,$2,$3,$4) ON CONFLICT(flight_id,scheduled_departure_at) DO NOTHING RETURNING id::text`, flight, aircraft, departure, departure.Add(time.Duration(route.minutes)*time.Minute)).Scan(&instance)
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					if err != nil {
						return result, err
					}
					var economy string
					for _, cabin := range []string{"ECONOMY", "BUSINESS"} {
						price := route.price + departureIndex*30000
						if cabin == "BUSINESS" {
							price *= 2
						}
						var fare string
						err = tx.QueryRow(ctx, `INSERT INTO fare_offers(flight_instance_id,fare_code,cabin,price_minor,taxes_minor,currency,refundable,baggage_allowance_kg,valid_until) VALUES($1,$2::text,$2::text,$3,$4,'EGP',$5,23,$6) RETURNING id::text`, instance, cabin, price, route.tax, cabin == "BUSINESS", departure).Scan(&fare)
						if err != nil {
							return result, err
						}
						if cabin == "ECONOMY" {
							economy = fare
						}
					}
					_, err = tx.Exec(ctx, `INSERT INTO flight_seats(flight_instance_id,aircraft_seat_id,state) SELECT $1,id,CASE WHEN seat_number IN ('2A','8C') THEN 'BLOCKED' ELSE 'AVAILABLE' END FROM aircraft_seats WHERE aircraft_type_id=$2`, instance, aircraftType)
					if err != nil {
						return result, err
					}
					var raw [5]byte
					if _, err = rand.Read(raw[:]); err != nil {
						return result, err
					}
					pnr := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
					// A synthetic confirmed reservation demonstrates unavailable seats. It is not a payment.
					_, err = tx.Exec(ctx, `WITH
			q AS (INSERT INTO price_quotes(user_id,request_hash,status,total_minor,currency,expires_at) SELECT $1::uuid,repeat('d',64),'USED',price_minor+taxes_minor,'EGP',clock_timestamp()+interval '10 minutes' FROM fare_offers WHERE id=$3::uuid RETURNING id,total_minor),
			qi AS (INSERT INTO price_quote_items(price_quote_id,flight_instance_id,fare_offer_id,passenger_count,unit_price_minor,taxes_minor,total_minor,currency) SELECT q.id,$2::uuid,f.id,1,f.price_minor,f.taxes_minor,q.total_minor,'EGP' FROM q,fare_offers f WHERE f.id=$3::uuid),
			b AS (INSERT INTO bookings(user_id,price_quote_id,pnr,status,total_minor,currency) SELECT $1::uuid,id,$4::text,'CONFIRMED',total_minor,'EGP' FROM q RETURNING id),
			p AS (INSERT INTO booking_passengers(booking_id,passenger_type,first_name,last_name) SELECT id,'ADULT','Fictional','Passenger' FROM b RETURNING id),
			sg AS (INSERT INTO booking_segments(booking_id,flight_instance_id,fare_offer_id,status,price_minor,taxes_minor,currency) SELECT b.id,$2::uuid,f.id,'CONFIRMED',f.price_minor,f.taxes_minor,'EGP' FROM b,fare_offers f WHERE f.id=$3::uuid RETURNING id),
			s AS (UPDATE flight_seats fs SET state='BOOKED',booking_id=b.id,version=version+1,updated_at=clock_timestamp() FROM b,aircraft_seats a WHERE fs.aircraft_seat_id=a.id AND fs.flight_instance_id=$2::uuid AND a.seat_number='5A' RETURNING fs.id)
			INSERT INTO seat_assignments(booking_id,booking_segment_id,flight_instance_id,passenger_id,flight_seat_id,status) SELECT b.id,sg.id,$2::uuid,p.id,s.id,'CONFIRMED' FROM b,sg,p,s`, owner, instance, economy, pnr)
					if err != nil {
						return result, err
					}
					result.FlightsAdded++
					result.SeatsAdded += 72
				}
			}
		}
	}
	return result, tx.Commit(ctx)
}
