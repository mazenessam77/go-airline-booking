-- +goose Up
ALTER TABLE idempotency_records ADD COLUMN response JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE idempotency_records DROP COLUMN response;
