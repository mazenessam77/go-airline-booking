\set ON_ERROR_STOP on
BEGIN;
DO $$ BEGIN
    IF current_database()<>'airline_booking_codex_test' THEN
        RAISE EXCEPTION 'role verification requires the dedicated test database';
    END IF;
END $$;
\ir roles.sql
\ir grants.sql
DO $$ BEGIN
    IF has_schema_privilege('airline_runtime','public','CREATE')
       OR has_table_privilege('airline_runtime','goose_db_version','SELECT')
       OR has_column_privilege('airline_runtime','users','role','UPDATE')
       OR has_column_privilege('airline_runtime','booking_passengers','travel_document_ciphertext','SELECT') THEN
        RAISE EXCEPTION 'runtime permissions are too broad';
    END IF;
    IF NOT has_table_privilege('airline_runtime','bookings','UPDATE')
       OR NOT has_table_privilege('airline_runtime','auth_credentials','INSERT') THEN
        RAISE EXCEPTION 'required runtime permissions are missing';
    END IF;
END $$;
SET LOCAL ROLE airline_runtime;
SELECT count(*) AS visible_flights FROM flight_instances;
SELECT count(*) AS visible_bookings FROM bookings;
ROLLBACK;
