-- +goose Up
-- Existing quotes/bookings have no provable historical policy; default to manual review.
ALTER TABLE price_quote_items ADD COLUMN refundable BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE booking_segments ADD COLUMN refundable BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE booking_segments DROP COLUMN refundable;
ALTER TABLE price_quote_items DROP COLUMN refundable;
