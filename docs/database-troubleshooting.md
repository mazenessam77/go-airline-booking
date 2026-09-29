# Database and connection pool troubleshooting

[Back to the lab](../README.md) · [Pool metrics](observability.md#database-pool-metrics)

## First, connect to the right database

The Compose service is `postgres`. On the Mac, its published address is `127.0.0.1:5433`. Inside the API/worker containers, use `postgres:5432`.

`localhost` means the network namespace of the process making the connection. Inside the API container, it refers to that container, not the Mac or PostgreSQL container. Compose's service name resolves on its container network.

| Variable | Used by | Example host/port |
| --- | --- | --- |
| `DATABASE_URL` in a host process | Host Go tools / Goose | `127.0.0.1:5433` |
| `CONTAINER_DATABASE_URL` in `.env` | Compose passes it as `DATABASE_URL` inside API and worker | `postgres:5432` |

Both should point to `airline_booking_demo` for these labs. Use [.env.example](../.env.example); keep actual credentials local. The seeder refuses any other database name at its command entry point. The test runner uses a separate `_test` database and can truncate/drop its fixtures.

## Read-only session inspection

Connect from the repository root without copying the password into a command:

```sh
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d airline_booking_demo'
```

In `psql`:

```sql
SELECT current_database(), current_user;
SHOW max_connections;

SELECT pid, application_name, state,
       clock_timestamp() - query_start AS query_age,
       clock_timestamp() - xact_start AS transaction_age,
       wait_event_type, wait_event,
       pg_blocking_pids(pid) AS blockers,
       left(query, 300) AS query
FROM pg_stat_activity
WHERE datname = current_database()
  AND pid <> pg_backend_pid()
ORDER BY query_start;
```

Run `\watch 1` after the activity query to sample every second while another terminal sends requests. Ctrl+C stops watching; `\q` exits. Activity text can contain sensitive information in other environments; these exercises use fictional local data.

For a compact count:

```sql
SELECT application_name, state, count(*)
FROM pg_stat_activity
WHERE datname = current_database()
GROUP BY application_name, state
ORDER BY application_name, state;
```

The shared pool constructor sets `application_name=go-airline-booking-api` for **both API and worker**. That name cannot distinguish them. Use PIDs/client context and the API-only pool metrics instead of assuming every session belongs to an HTTP request.

## Interpret the evidence

- `active` means a statement is executing, including time spent waiting. It does not mean the database CPU is busy.
- `wait_event_type='Timeout'` with `wait_event='PgSleep'` means the statement called `pg_sleep()`. It's waiting on a timer, not on a lock, I/O, or CPU.
- Nonempty `pg_blocking_pids` identifies blockers; lock waits are different from a timer wait.
- `idle` can be a normal reusable pool connection. `idle in transaction` deserves attention because an unfinished transaction can retain resources/locks.
- A high pool acquisition time measures getting a connection. SQL can be slow even when acquisition is immediate.

The pool defaults to max 20/min 2 connections **per process**, a 30-minute maximum lifetime, and five-minute idle time. API and worker each have a pool; replicas multiply demand. API connections set a five-second statement timeout, 1.5-second lock timeout, and five-second idle-in-transaction timeout. The HTTP deadline defaults to ten seconds. These are separate budgets, not a guarantee that every request finishes quickly.

`SHOW statement_timeout` in your administrative `psql` session shows that session's value, not the API connection's setting. Read [pool configuration](../internal/database/postgres.go) for the application settings.

## Query plans and data correctness

Start with session evidence and read-only `EXPLAIN`. `EXPLAIN ANALYZE` actually executes its statement; use controlled SELECTs in the dedicated lab/test database. [Performance verification](performance.md) documents the existing opt-in query-plan fixture and benchmarks.

The seat-hold tests cover two bookings racing for one seat, atomic rollback of multi-seat failures, ownership, expiry reclamation, repeated holds, and overlapping expiration workers. Row locks and unique indexes are intentional correctness controls. Do not “fix” latency by removing them.

PostgreSQL remains locally bound because EC2 needs the API's metrics, not direct database access. Run database diagnostics on the Mac or through `docker compose exec`; no remote PostgreSQL listener is needed.
