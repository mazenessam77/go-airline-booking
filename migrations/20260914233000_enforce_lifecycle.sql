-- +goose Up
ALTER TABLE bookings ADD CONSTRAINT bookings_active_expiration_check
    CHECK (status NOT IN ('HELD','PAYMENT_PENDING') OR hold_expires_at IS NOT NULL);
ALTER TABLE booking_passengers ADD CONSTRAINT booking_passengers_name_lengths
    CHECK (length(first_name) BETWEEN 1 AND 100 AND length(last_name) BETWEEN 1 AND 100);

-- +goose StatementBegin
CREATE FUNCTION enforce_booking_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = OLD.status THEN RETURN NEW; END IF;
    IF (OLD.status = 'DRAFT' AND NEW.status IN ('HELD','CANCELLED','EXPIRED'))
       OR (OLD.status = 'HELD' AND NEW.status IN ('DRAFT','PAYMENT_PENDING','CANCELLED','EXPIRED'))
       OR (OLD.status = 'PAYMENT_PENDING' AND NEW.status IN ('CONFIRMED','CANCELLED','EXPIRED'))
       OR (OLD.status = 'CONFIRMED' AND NEW.status IN ('TICKETED','CANCELLED'))
       OR (OLD.status = 'TICKETED' AND NEW.status = 'CANCELLED')
       OR (OLD.status = 'CANCELLED' AND NEW.status = 'REFUNDED') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invalid booking transition' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bookings_transition_guard BEFORE UPDATE OF status ON bookings
FOR EACH ROW EXECUTE FUNCTION enforce_booking_transition();

-- +goose Down
DROP TRIGGER bookings_transition_guard ON bookings;
DROP FUNCTION enforce_booking_transition();
ALTER TABLE booking_passengers DROP CONSTRAINT booking_passengers_name_lengths;
ALTER TABLE bookings DROP CONSTRAINT bookings_active_expiration_check;
