#!/bin/sh

set -eu

B=${B:-http://127.0.0.1:8080}
ORIGIN=${ORIGIN:-CAI}
DEST=${DEST:-JED}
DATE=${DATE:-$(date -v+1d +%F 2>/dev/null || date -d tomorrow +%F)}

fail() { printf 'lab setup: %s\n' "$1" >&2; exit 1; }
post() { path=$1; shift; curl -sS --fail-with-body -X POST "$B$path" -H 'Content-Type: application/json' "$@"; }

email="lab+$(uuidgen | tr 'A-Z' 'a-z')@example.test"
password="$(uuidgen)-$(uuidgen)"
post /v1/auth/register --data "{\"email\":\"$email\",\"password\":\"$password\",\"first_name\":\"Lab\",\"last_name\":\"User\"}" >/dev/null || fail "register failed"
TOKEN=$(post /v1/auth/login --data "{\"email\":\"$email\",\"password\":\"$password\"}" | jq -r .access_token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || fail "login failed"
auth="Authorization: Bearer $TOKEN"

FLIGHT_ID=$(curl -sS "$B/v1/flights?origin=$ORIGIN&destination=$DEST&date=$DATE" | jq -r '.items[0].id // empty')
[ -n "$FLIGHT_ID" ] || fail "no flights for $ORIGIN-$DEST on $DATE (seed demo data?)"
fare=$(curl -sS "$B/v1/flight-instances/$FLIGHT_ID" | jq -c '.fares[0]')
FARE_ID=$(printf %s "$fare" | jq -r .id)
CABIN=$(printf %s "$fare" | jq -r .cabin)

QUOTE_ID=$(post /v1/quotes -H "$auth" --data "{\"items\":[{\"fare_offer_id\":\"$FARE_ID\",\"passenger_count\":1}]}" | jq -r .id)
BOOKING_ID=$(post /v1/bookings -H "$auth" -H "Idempotency-Key: lab-$(uuidgen)" --data "{\"quote_id\":\"$QUOTE_ID\"}" | jq -r .id)
post "/v1/bookings/$BOOKING_ID/passengers" -H "$auth" -H "Idempotency-Key: lab-$(uuidgen)" \
  --data '{"passenger_type":"ADULT","first_name":"Lab","last_name":"Passenger","date_of_birth":"1990-01-01"}' >/dev/null || fail "add passenger failed"

detail=$(curl -sS "$B/v1/bookings/$BOOKING_ID" -H "$auth")
SEGMENT_ID=$(printf %s "$detail" | jq -r '.segments[0].id')
PASSENGER_ID=$(printf %s "$detail" | jq -r '.passengers[0].id')
SEAT_ID=$(curl -sS "$B/v1/flight-instances/$FLIGHT_ID/seats?limit=100" |
  jq -r --arg c "$CABIN" '[.items[] | select(.cabin == $c and .availability == "AVAILABLE")][0].id // empty')
[ -n "$SEAT_ID" ] || fail "no available $CABIN seat on $FLIGHT_ID"

cat <<EOF
export B='$B' TOKEN='$TOKEN' BOOKING_ID='$BOOKING_ID' SEGMENT_ID='$SEGMENT_ID'
export FLIGHT_ID='$FLIGHT_ID' PASSENGER_ID='$PASSENGER_ID' SEAT_ID='$SEAT_ID'
export HOLD_BODY='{"booking_segment_id":"$SEGMENT_ID","flight_instance_id":"$FLIGHT_ID","seats":[{"passenger_id":"$PASSENGER_ID","flight_seat_id":"$SEAT_ID"}]}'
EOF
