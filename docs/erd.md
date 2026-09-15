# Entity relationships

```mermaid
erDiagram
    airports ||--o{ flights : origin_destination
    aircraft_types ||--o{ aircraft : type
    aircraft_types ||--o{ aircraft_seats : layout
    aircraft ||--o{ flight_instances : operates
    flights ||--o{ flight_instances : schedules
    flight_instances ||--o{ fare_offers : prices
    flight_instances ||--o{ flight_seats : inventory
    aircraft_seats ||--o{ flight_seats : position
    users ||--o{ auth_sessions : authenticates
    auth_sessions ||--o{ auth_credentials : hashes
    users ||--o{ security_audit_records : events
    users ||--o{ price_quotes : owns
    price_quotes ||--|{ price_quote_items : contains
    fare_offers ||--o{ price_quote_items : snapshots
    price_quotes ||--o| bookings : consumed_once
    users ||--o{ bookings : owns
    bookings ||--o{ booking_passengers : carries
    bookings ||--|{ booking_segments : itinerary
    booking_segments ||--o{ seat_assignments : assigns
    booking_passengers ||--o{ seat_assignments : occupies
    flight_seats ||--o{ seat_assignments : history
    bookings ||--o| payments : charges
    payments ||--o{ payment_attempts : retries
    payments ||--o{ webhook_events : deduplicates
    payments ||--o| refunds : reverses
    users ||--o{ idempotency_records : scopes
    outbox_events ||--o{ outbox_receipts : acknowledges
```

Only one active assignment may exist per seat or passenger/segment. An outbox aggregate
ID references a booking or payment logically, allowing one queue for both domain types.
