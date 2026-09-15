# Performance verification

Run benchmarks only with TEST_DATABASE_URL pointing to a disposable `_test` database:

```sh
go test -run '^$' -bench . -benchmem ./internal/flight ./internal/booking
```

The microbenchmarks use small deterministic fixtures. They are regression tools, not
production throughput estimates. Search uses bounded keyset queries. Seat acquisition
sorts UUIDs before locking; database statement and lock deadlines bound contention.

Before sizing production, populate an isolated database with representative routes,
departure distributions, cabin layouts, active/expired holds and historical assignments.
Run EXPLAIN (ANALYZE, BUFFERS) for search, seat maps, held-seat acquisition and worker
claim queries. Record plans with PostgreSQL settings, row counts and cache state.
Existing route, departure, availability, owner and partial active-assignment indexes
are retained. Do not add speculative indexes based only on an empty development database.

## Local measurements, 2026-09-14

Go 1.26.8 on Apple M1, with PostgreSQL 17 in local Docker over TCP. A 100-iteration
small-fixture run measured flight search at 472,219 ns/op (4,711 B/op, 31 allocations)
and existing-hold requests at 4,789,961 ns/op (13,910 B/op, 240 allocations).
The latter exercises an existing hold, not first-time seat acquisition under contention.

The opt-in EXPLAIN fixture contains 50,001 flight instances. The parameterized
route/date query returned 20 rows in 0.221 ms server execution time, using
flights_route_idx and flight_instances_flight_id_scheduled_departure_at_key with
departure bounds in the index condition. This result supports retaining those indexes;
it does not establish capacity for a realistic multi-route production workload.
An initial diagnostic using volatile clock expressions for date bounds scanned extra
rows; the application already binds date values, and the recorded result uses that form.

To reproduce the optional checks with the local test runner:

```sh
GOOSE_BIN="$(go env GOPATH)/bin/goose" RUN_BENCHMARKS=1 RUN_QUERY_PLANS=1 sh scripts/test-local.sh
```

Load-test a local/staging deployment with a tool such as k6 or Vegeta. Start with bounded
search traffic, then test many different bookings competing for one seat and overlapping
seat sets. Use a distinct authenticated user/booking and idempotency key per logical
operation, and reuse the same key for retries. Never use production passenger data.
Track p50/p95/p99 latency, successful holds, 409 conflicts, 503 retries, connection-pool
wait, lock wait, deadlocks, CPU, memory and outbox lag. Verify exactly one active assignment
for each seat and verify that losing multi-seat requests leave no partial inventory changes.

Exercise replica restarts during outbox delivery, worker overlap, refresh-token reuse,
payment timeouts and late success with the fake provider. Real-provider sandbox testing
requires credentials and explicit authorization before connecting an external service.
