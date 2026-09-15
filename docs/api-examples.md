# API examples

Values below are placeholders, not credentials. Supply ACCESS_TOKEN, QUOTE_ID,
BOOKING_ID, SEGMENT_ID, FLIGHT_ID, PASSENGER_ID and SEAT_ID from your own test responses.
Do not paste real credentials or passenger data into shared transcripts.

```sh
curl --fail-with-body http://127.0.0.1:8080/v1/flights'?origin=AAA&destination=BBB&date=2026-10-01&limit=20'

curl --fail-with-body -X POST http://127.0.0.1:8080/v1/quotes \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  --data '{"items":[{"fare_offer_id":"11111111-1111-4111-8111-111111111111","passenger_count":1}]}'

curl --fail-with-body -X POST http://127.0.0.1:8080/v1/bookings \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-booking-request-001' \
  --data "{\"quote_id\":\"$QUOTE_ID\"}"

curl --fail-with-body -X POST "http://127.0.0.1:8080/v1/bookings/$BOOKING_ID/passengers" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-passenger-request-001' \
  --data '{"passenger_type":"ADULT","first_name":"Example","last_name":"Passenger","date_of_birth":"1990-01-01"}'

curl --fail-with-body -X POST "http://127.0.0.1:8080/v1/bookings/$BOOKING_ID/seat-holds" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-seat-hold-request-001' \
  --data "{\"booking_segment_id\":\"$SEGMENT_ID\",\"flight_instance_id\":\"$FLIGHT_ID\",\"seats\":[{\"passenger_id\":\"$PASSENGER_ID\",\"flight_seat_id\":\"$SEAT_ID\"}]}"
```

Reuse a key only for an identical request. A conflicting payload receives
`idempotency_conflict`. The API never accepts prices, totals, roles, hold expiry or
user IDs in these request bodies. Payment and refund HTTP endpoints currently return
`payments_disabled`; no real provider is connected.
