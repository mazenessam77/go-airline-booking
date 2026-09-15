#!/bin/sh
set -eu

# Run from the repository root. Do not enable shell tracing: credentials stay in memory.
docker compose up -d postgres
PGUSER=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_USER"')
PGPASSWORD=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_PASSWORD"')
export PGUSER PGPASSWORD
export PGHOST=127.0.0.1 PGPORT=5433 PGDATABASE=airline_booking_demo
export DATABASE_URL='postgres://127.0.0.1:5433/airline_booking_demo?sslmode=disable'
export APP_ENV=development
export HTTP_ADDR=${DEMO_HTTP_ADDR:-127.0.0.1:8080}
export GOOSE_DRIVER=postgres GOOSE_DBSTRING="$DATABASE_URL"

attempt=0
until pg_isready -q; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 30 ]; then printf '%s\n' 'PostgreSQL did not become ready.' >&2; exit 1; fi
    sleep 1
done
exists=$(psql -d postgres -Atc "SELECT 1 FROM pg_database WHERE datname='airline_booking_demo'")
if [ "$exists" != 1 ]; then createdb airline_booking_demo; fi
actual=$(psql -Atc 'SELECT current_database()')
if [ "$actual" != airline_booking_demo ]; then printf '%s\n' 'Refusing to modify another database.' >&2; exit 1; fi
goose_bin=${GOOSE_BIN:-goose}
"$goose_bin" -dir migrations up
go run ./cmd/seed-demo
if [ "${DEMO_SEED_ONLY:-0}" = 1 ]; then exit 0; fi
go build -o bin/demo-api ./cmd/api
go build -o bin/demo-worker ./cmd/worker
api_pid=
worker_pid=
cleanup() {
    trap - EXIT INT TERM
    if [ -n "$api_pid" ]; then kill "$api_pid" 2>/dev/null || true; fi
    if [ -n "$worker_pid" ]; then kill "$worker_pid" 2>/dev/null || true; fi
    wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM
bin/demo-worker &
worker_pid=$!
bin/demo-api &
api_pid=$!
printf 'Open http://%s — choose Try a demo account. Press Ctrl+C to stop.\n' "$HTTP_ADDR"
wait "$api_pid"
