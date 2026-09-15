#!/bin/sh
set -eu
PGUSER=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_USER"')
PGPASSWORD=$(docker compose exec -T postgres sh -c 'printf %s "$POSTGRES_PASSWORD"')
export PGUSER PGPASSWORD
export APP_ENV=test
export DATABASE_URL='postgres://127.0.0.1:5433/airline_booking_codex_test?sslmode=disable'
export HTTP_ADDR=127.0.0.1:18080
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
smoke_logs=$(mktemp -d)
bin/api >"$smoke_logs/api.log" 2>&1 &
api_pid=$!
bin/worker >"$smoke_logs/worker.log" 2>&1 &
worker_pid=$!
cleanup() {
    kill -TERM "$api_pid" "$worker_pid" 2>/dev/null || true
    wait "$api_pid" || true
    wait "$worker_pid" || true
}
trap cleanup EXIT INT TERM
attempt=0
until curl --silent --fail http://127.0.0.1:18080/readyz; do
    kill -0 "$api_pid" "$worker_pid"
    attempt=$((attempt+1))
    if [ "$attempt" -gt 30 ]; then exit 1; fi
    sleep 0.5
done
curl --silent --fail http://127.0.0.1:18080/healthz
kill -0 "$worker_pid"
printf '\nSmoke logs: %s\n' "$smoke_logs"
