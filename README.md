# Go airline booking backend

Go 1.26.8, PostgreSQL 17, pgx, goose, net/http and structured slog logging.
The API supports public flight search, authenticated trusted quotes, booking creation,
passengers and atomic seat holds. A separate worker expires holds and processes
transactional outbox events. Payment and refund services have a fake test provider;
real payment HTTP processing is deliberately disabled until a reviewed adapter is configured.

## Browser demo

The API serves a lightweight same-origin frontend at `/`. It searches real PostgreSQL
inventory, creates real authenticated bookings, adds fictional passengers, and exercises
the race-safe seat hold/release flow. Access and refresh tokens remain in browser memory;
they are not persisted in local storage or cookies.

Start the complete demo from the repository root:

```sh
GOOSE_BIN="$(go env GOPATH)/bin/goose" sh scripts/demo-local.sh
```

Then open <http://127.0.0.1:8080> and select **Try a demo account**. The command creates
and migrates only `airline_booking_demo`, seeds seven days of fictional Cairo routes, and
starts both the API and hold-expiration worker. Seeding is idempotent and preserves any
bookings already created in that database. Press Ctrl+C to stop both processes.

The fixture has 112 departures and 8,064 seats across Cairo, Dubai, London, Jeddah, and
Aswan. Every flight includes blocked and booked seats so unavailable inventory is visible.
No shared demo password is stored: the button registers a fresh random account through the
real auth API. Passenger names must remain fictional. Payments are intentionally unavailable,
and the frontend never asks for card data.

Run the dependency-free end-to-end HTTP smoke journey while the demo is running:

```sh
node scripts/ui-smoke.mjs
```

It verifies static assets, flight search, trusted fares, registration/login, quote and
booking creation, idempotent replay, passenger creation, atomic multi-seat holds, release,
cancellation, owned booking listing, and wrong-owner resource hiding.

## Local development

Install Go 1.26.8, Docker Compose and the PostgreSQL client tools. Install goose:

```sh
go install github.com/pressly/goose/v3/cmd/goose@v3.28.0
export PATH="$(go env GOPATH)/bin:$PATH"
```

Set POSTGRES_DB, POSTGRES_USER and POSTGRES_PASSWORD in your shell or an ignored local
`.env` file for Compose. Supply DATABASE_URL separately in the API process environment.
Go does not load `.env` automatically. Never commit actual credentials.

```sh
docker compose up -d postgres
# DATABASE_URL must identify the local development database on port 5433.
# URL shape: postgres://USER:PASSWORD@127.0.0.1:5433/DATABASE?sslmode=disable
goose -dir migrations postgres "$DATABASE_URL" up
go run ./cmd/api
```

In another terminal with the same DATABASE_URL:

```sh
go run ./cmd/worker
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
```

For containerized API/worker, set CONTAINER_DATABASE_URL with host `postgres` and port
5432, apply migrations first, then run `docker compose --profile app up -d --build`.
Both processes run as a non-root user with a read-only filesystem. No process runs migrations.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| APP_ENV | development | development, test, production |
| DATABASE_URL | required | PostgreSQL connection string |
| HTTP_ADDR | :8080 | HTTP listen address |
| DB_MAX_CONNS | 20 | Maximum connections per process |
| DB_MIN_CONNS | 2 | Minimum connections per process |
| REQUEST_TIMEOUT | 10s | Request deadline, 1s–30s |
| REQUEST_BODY_LIMIT | 65536 | JSON body bytes, 1024–1048576 |
| CORS_ALLOWED_ORIGINS | empty | Comma-separated exact origins |
| TRUSTED_PROXY_CIDRS | empty | Explicit trusted ingress networks |
| TEST_DATABASE_URL | empty | Integration tests only; database name must end in `_test` |

Production requires `sslmode=verify-full` and `sslrootcert` in a PostgreSQL URL.
Local plaintext connections are only for local development. Budget connection pools
across all API and worker replicas, leaving capacity for migrations and operations.

## Verification

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Without TEST_DATABASE_URL, PostgreSQL integration tests explicitly skip. To run tests
and a complete migration up/down/up round-trip against the existing local Compose server:

```sh
GOOSE_BIN="$(go env GOPATH)/bin/goose" CHECK_MIGRATION_ROUNDTRIP=1 sh scripts/test-local.sh
```

The script creates and reuses **airline_booking_codex_test**, reads local Compose
credentials without printing them, and operates only in that database. It truncates
test fixtures and optionally drops/recreates its schema through goose. Never put
valuable data in that dedicated test database. Application databases are not touched.
Test packages share a PostgreSQL advisory lock so fixtures cannot collide across suites.
Tests also verify the connected database name before destructive cleanup.

For an independently provisioned test database, set TEST_DATABASE_URL and run:

```sh
GOOSE_DRIVER=postgres GOOSE_DBSTRING="$TEST_DATABASE_URL" goose -dir migrations up
go test -race -count=10 ./internal/booking/...
go test -race -shuffle=on ./...
```

CI checks formatting, vet, unit/integration tests, race tests, migration round-trips,
builds and govulncheck using an isolated PostgreSQL service. A successful push to `main`
then scans the exact release image, pushes it to private ECR, and deploys its immutable
digest to the single configured EC2 instance through SSM. See the
[AWS deployment runbook](docs/aws-deployment.md).

## API

See [OpenAPI 3.1](api/openapi.json) and [request examples](docs/api-examples.md).
All domain routes use `/v1`. Authentication uses `Authorization: Bearer TOKEN`.
Mutations for bookings, passengers, holds, payments and refunds require Idempotency-Key.
Unknown JSON fields and multiple documents are rejected. An authenticated wrong owner
receives the same 404 envelope as a missing booking. Responses use UTC RFC3339 dates
and integer minor currency units.

See [architecture decisions](docs/architecture.md), [ERD](docs/erd.md),
[security operations](docs/security.md), [performance testing](docs/performance.md),
and [implementation status](docs/delivery.md). Production readiness still requires the
external integrations and operational gates listed there.
