#!/bin/sh
set -eu

# Read credentials without printing or persisting them. Never enable shell tracing.
PGUSER=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_USER"')
PGPASSWORD=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_PASSWORD"')
export PGUSER PGPASSWORD
export PGHOST=127.0.0.1 PGPORT=5433
export PGDATABASE=airline_booking_codex_test
export TEST_DATABASE_URL='postgres://127.0.0.1:5433/airline_booking_codex_test?sslmode=disable'
export GOOSE_DRIVER=postgres GOOSE_DBSTRING="$TEST_DATABASE_URL"

exists=$(psql -d postgres -Atc "SELECT 1 FROM pg_database WHERE datname='airline_booking_codex_test'")
if [ "$exists" != 1 ]; then
    createdb airline_booking_codex_test
fi

actual=$(psql -Atc 'SELECT current_database()')
if [ "$actual" != airline_booking_codex_test ]; then
    printf '%s\n' 'Refusing to operate outside the dedicated test database.' >&2
    exit 1
fi

goose_bin=${GOOSE_BIN:-goose}
"$goose_bin" -dir migrations validate
"$goose_bin" -dir migrations up
go test -race -p 1 -count=1 ./...
if [ "${CHECK_MIGRATION_ROUNDTRIP:-0}" = 1 ]; then
    "$goose_bin" -dir migrations down-to 0
    "$goose_bin" -dir migrations up
    go test -race -p 1 -count=1 ./...
fi

if [ "${RUN_BENCHMARKS:-0}" = 1 ]; then
    go test -p 1 -run '^$' -bench . -benchtime=100x -benchmem ./internal/flight ./internal/booking
fi
if [ "${RUN_QUERY_PLANS:-0}" = 1 ]; then
    go test -run TestSearchExplain -v ./internal/flight
fi
if [ "${CHECK_RUNTIME_GRANTS:-0}" = 1 ]; then
    psql -v ON_ERROR_STOP=1 -f ops/check-runtime.sql
fi
