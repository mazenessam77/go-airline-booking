# Run the local incident lab

[Back to the lab](../README.md) · [CPU lab](labs/cpu.md) · [Booking lab](labs/booking-latency.md)

Use a disposable local demo database and fictional passengers. The faults are off unless named in `LAB_FAULTS`, a comma-separated environment variable read when the API starts. The available names in the code are `cpu`, `latency` (flight details), and `booking-latency`. Enable one at a time while learning so the evidence stays clear.

## Prerequisites

- Docker Desktop/Engine with `docker compose`, Go 1.26.8 (the version in `go.mod`), PostgreSQL client tools (`psql`, `createdb`, `pg_isready`), `curl` with `--fail-with-body`, `jq`, and `uuidgen`.
- Goose v3.28.0 for migrations. Node.js is optional for `scripts/ui-smoke.mjs`; `hey` is optional for the CPU load example.
- Run commands from the repository root. Keep Docker's CPU/memory allocation in mind when comparing measurements.

```sh
go install github.com/pressly/goose/v3/cmd/goose@v3.28.0
export PATH="$(go env GOPATH)/bin:$PATH"
test -e .env || cp .env.example .env
```

Edit the ignored `.env` to use a unique local password in `POSTGRES_PASSWORD`, `DATABASE_URL`, and `CONTAINER_DATABASE_URL`. Keep `POSTGRES_DB=airline_booking_demo`. The example file contains only a placeholder password. The host URL reaches `127.0.0.1:5433`; the container URL reaches `postgres:5432`. [Why those differ](database-troubleshooting.md#first-connect-to-the-right-database).

Compose reads `.env` for substitution. `go run`, Goose, and shell tools do not load it automatically. The helpers read the running PostgreSQL container's configured credentials without printing them. If your Compose PostgreSQL volume already exists, changing `.env` does **not** change that database role's password; use the credentials that actually initialize the volume, or a fresh dedicated volume. Do not run migrations against a valuable database.

## Start PostgreSQL, migrate, seed

```sh
docker compose up -d postgres
GOOSE_BIN="$(go env GOPATH)/bin/goose" DEMO_SEED_ONLY=1 sh scripts/demo-local.sh
```

The helper creates `airline_booking_demo` if needed, applies Goose migrations, then seeds fictional flights for the next seven UTC days. It preserves previously created bookings and inventory. `cmd/seed-demo` refuses other database names. The seed contains Cairo, Dubai, London, Jeddah, and Aswan routes, with some unavailable seats for the browser demo.

For a host-side `goose` command outside this helper, load a host `DATABASE_URL` into that shell first. The string in `.env` is not automatically present as a shell variable.

## Start the API and worker in Docker

```sh
LAB_FAULTS= docker compose --profile app up -d --build api worker
export B=http://127.0.0.1:8080
curl --fail-with-body "$B/healthz"
curl --fail-with-body "$B/readyz"
curl --fail-with-body "$B/metrics"
```

The default `API_BIND_ADDR=127.0.0.1` exposes port 8080 only on the Mac. `docker compose ps` shows the actual mapping. The worker expires holds and processes outbox receipts; it has a separate database pool. Open `$B/` for the embedded browser demo. The **Try a demo account** button creates a fictional account through the real auth API; it does not use a shared password.

For the existing host-run demo instead of the Compose API and worker:

```sh
GOOSE_BIN="$(go env GOPATH)/bin/goose" sh scripts/demo-local.sh
```

That script starts the host API/worker after seeding and keeps them in the foreground until Ctrl+C. Do not run it at the same time as the Compose API on the same port. The host API uses `127.0.0.1:5433`; exported process metrics may differ from Linux container metrics on macOS.

## Exercise the API

A valid public search needs `origin`, `destination`, and `date`:

```sh
export DATE=$(date -u -v+1d +%F 2>/dev/null || date -u -d tomorrow +%F)
export Q="origin=CAI&destination=JED&date=$DATE&limit=20"
curl --fail-with-body -i "$B/v1/flights?$Q"
```

For an authenticated quote → booking → passenger → seat selection, use the [booking setup helper](labs/booking-latency.md#prepare-a-real-booking). For the browser/API smoke journey against the fault-free demo:

```sh
node scripts/ui-smoke.mjs
```

The HTTP smoke script creates its own account and checks search, quote, booking, idempotent hold/replay, release, cancellation, and wrong-owner hiding. It expects seeded `CAI`–`DXB` flights. With a fault enabled, its timing and ten-second request deadline may change the outcome; use it as a functional baseline with faults off.

## Enable and clear faults

```sh
LAB_FAULTS=cpu docker compose --profile app up -d --force-recreate api
LAB_FAULTS=booking-latency docker compose --profile app up -d --force-recreate api
LAB_FAULTS= docker compose --profile app up -d --force-recreate api
```

Run one of those commands at a time. Confirm readiness after each recreate. The active fault names appear in the `HTTP server started` log. `latency` affects `GET /v1/flight-instances/{id}` with a randomized 1.5–2.5-second context-aware wait; the two full investigations focus on CPU and booking latency. With default `REQUEST_TIMEOUT=10s`, enough contention can produce a timeout even when the fault itself requests a shorter wait.

The [CPU guide](labs/cpu.md) uses valid search traffic. The [booking guide](labs/booking-latency.md) prepares authenticated requests, checks the required idempotency key, and bounds repeat traffic.

## View metrics and optionally monitor remotely

```sh
curl --fail-with-body "$B/metrics"
docker compose logs --tail 50 api
docker stats
```

Read [observability](observability.md) before interpreting the counters and gauges. For the reported Mac-to-EC2 setup, set your own Tailscale address in `API_BIND_ADDR`, follow [private networking](networking.md), then [Prometheus/Grafana](monitoring/prometheus-grafana.md). PostgreSQL stays local. Remote monitoring is optional; the lab can be run entirely on your machine.

## Verify code and concurrency behavior

```sh
go test ./...
```

Integration tests skip without `TEST_DATABASE_URL`. To run them against the dedicated local `_test` database, use the repository's existing runner after reading its cleanup behavior:

```sh
GOOSE_BIN="$(go env GOPATH)/bin/goose" sh scripts/test-local.sh
```

The script creates/reuses `airline_booking_codex_test`, migrates it, and tests under `-race`. Test helpers verify the database name before truncating fixtures. Set `CHECK_MIGRATION_ROUNDTRIP=1` only when you intend to run its drop/recreate migration round-trip on that dedicated test database. Seat-hold integration tests cover competing bookings, repeated holds, rollback, expiry, and concurrent workers.
