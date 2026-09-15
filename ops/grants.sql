-- Run as the migration owner after migrations, in the target database.
GRANT SELECT ON airports,aircraft_types,aircraft,aircraft_seats,flights,flight_instances,fare_offers TO airline_runtime;
-- SHARE row locks require UPDATE permission on at least one column of each table.
GRANT UPDATE(updated_at) ON flight_instances,fare_offers TO airline_runtime;
GRANT SELECT,INSERT,UPDATE ON price_quotes,price_quote_items,bookings,booking_segments,flight_seats,seat_assignments TO airline_runtime;
GRANT SELECT(id,booking_id,passenger_type,first_name,last_name,date_of_birth,nationality,created_at),
      INSERT(booking_id,passenger_type,first_name,last_name,date_of_birth),
      UPDATE(passenger_type,first_name,last_name,date_of_birth)
ON booking_passengers TO airline_runtime;
GRANT SELECT ON users TO airline_runtime;
GRANT INSERT(email,password_hash,first_name,last_name),UPDATE(updated_at) ON users TO airline_runtime;
GRANT SELECT,INSERT,UPDATE ON auth_sessions,auth_credentials,rate_limit_buckets,idempotency_records,
    payments,payment_attempts,webhook_events,refunds,outbox_events,outbox_receipts TO airline_runtime;
GRANT INSERT ON security_audit_records TO airline_runtime;
GRANT USAGE ON SEQUENCE security_audit_records_id_seq TO airline_runtime;
-- No table ownership, schema creation, migration version access, DELETE, or role mutation.
-- Apply these explicit grants again after adding tables; avoid broad default privileges.
