# Lab 2: Seat-hold latency incident

[Back to the lab](../../README.md) · [Metrics and PromQL](../observability.md) · [Database troubleshooting](../database-troubleshooting.md)

Work through the sections in order. The cause is near the end on purpose. Statements are labeled **Code fact** (readable in this repository), **Lab observation** (one run on one machine, and yours will differ), or **Lesson**.

## 1. Symptom

> "Customers say *Hold seat* spins for a couple of seconds before it confirms. It does work, and nobody lost a seat. Everything else feels normal. Monitoring is all green."

The affected call is `POST /v1/bookings/{booking_id}/seat-holds`. Keep "monitoring is all green" in mind. It's the most important clue in the ticket.

## 2. Reproduce

Finish [local setup](../running.md) first, including demo data. Then enable the scenario:

```sh
export B=${B:-http://127.0.0.1:8080}
export DATE=$(date -u -v+1d +%F 2>/dev/null || date -u -d tomorrow +%F)
LAB_FAULTS=booking-latency docker compose --profile app up -d --force-recreate api
curl --fail-with-body "$B/readyz"               # retry until ready
docker compose logs api | grep lab_faults       # should show ["booking-latency"]
```

`docker compose restart` does **not** apply a new `LAB_FAULTS` value. The container has to be recreated.

### Prepare a real booking

A seat hold needs a logged-in user, a quote, a DRAFT booking, a passenger, and a free seat in the right cabin. [scripts/lab-booking-setup.sh](../../scripts/lab-booking-setup.sh) does all of that with a random fictional account and prints shell exports (`TOKEN`, `BOOKING_ID`, `SEGMENT_ID`, `FLIGHT_ID`, `PASSENGER_ID`, `SEAT_ID`, `HOLD_BODY`). It does not hold the seat.

```sh
out=$(B="$B" DATE="$DATE" sh scripts/lab-booking-setup.sh) && eval "$out"; unset out
echo "$BOOKING_ID"
```

If the script prints `lab setup: …`, fix that first. Don't carry on with variables left over from an earlier run. `TOKEN` is a live access token: don't paste it anywhere. It expires after 10 minutes, so rerun the script when you get a 401.

### Send one hold

```sh
curl -sS -D - -o /dev/null -X POST "$B/v1/bookings/$BOOKING_ID/seat-holds" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(uuidgen)" --data "$HOLD_BODY" \
  -w 'status=%{http_code} connect=%{time_connect}s ttfb=%{time_starttransfer}s total=%{time_total}s\n' \
  | grep -iE '^x-request-id|^status='
```

Expect `status=200`. Write down the `X-Request-ID`.

**Code fact:** `Idempotency-Key` must be 16–128 printable ASCII characters (`internal/idempotency/idempotency.go`). A short key like `test1` gets a fast `400 invalid_input` and never reaches the hold logic, which is the same trap as the [HTTP 400 load test](cpu.md#the-fast-load-test-that-was-all-400s). Repeating a hold for the same passenger and seat is valid and returns 200 again.

## 3. Measure

**Lab observation** (fault on, one run):

```text
status=200 connect=0.000280s ttfb=2.351411s total=2.352500s
status=200 connect=0.000290s ttfb=2.177644s total=2.178202s
status=200 connect=0.000252s ttfb=1.200070s total=1.200471s
```

How to read those numbers:

- **Connect time is under a millisecond.** The network path isn't the problem.
- **TTFB is almost the whole total.** The time passes before the server sends anything, not while the body downloads. (Part of the reason: `http.TimeoutHandler` buffers the whole response, so the first byte leaves only when the handler finishes.)
- **Latency varies**, from 1.2 to 2.4 s, instead of sitting at a fixed value.

Now compare nearby endpoints on the same booking:

```sh
curl -sS -o /dev/null -H "Authorization: Bearer $TOKEN" \
  -w 'booking status=%{http_code} total=%{time_total}s\n' "$B/v1/bookings/$BOOKING_ID"
curl -sS -o /dev/null -H "Authorization: Bearer $TOKEN" \
  -w 'seats   status=%{http_code} total=%{time_total}s\n' "$B/v1/bookings/$BOOKING_ID/seats"
curl -sS -o /dev/null -w 'healthz=%{http_code} ' "$B/healthz"; curl -sS -o /dev/null -w 'readyz=%{http_code}\n' "$B/readyz"
```

**Lab observation:** booking GET ~7 ms, seats GET ~4 ms, releasing the hold ~6 ms, `healthz=200 readyz=200`. The same user, token, booking, and database were all fast on every other call.

## 4. Gather evidence

Collect these while a hold is actually running. Start a hold in one terminal and take the samples in another during the ~2-second window, or use the loop in [generating steady traffic](#generating-steady-traffic).

**Access log for the request ID:**

```sh
REQUEST_ID=replace-with-your-x-request-id
docker compose logs --no-log-prefix api | grep "$REQUEST_ID"
```

**Lab observation:** `"status":200,"duration_ms":2347`. The server measured the same ~2.35 s as the client, so the time is spent inside the API's handling. The log has no per-stage timing, and tracing isn't wired in, so it can't tell you *which* stage.

**Resources:**

```sh
docker stats --no-stream go-airline-booking-api-1 airline-postgres
curl -s "$B/metrics" | grep -E '^(db_pool_acquired_connections|db_pool_max_connections|http_requests_in_flight)'
```

**Lab observation:** API CPU ~0.5%, PostgreSQL ~2.9%, `db_pool_acquired_connections 1` out of a max of 20, and `http_requests_in_flight 1`. Nothing is busy, and one request has one connection checked out.

**PostgreSQL:** open `psql` and run the [session inspection query](../database-troubleshooting.md#read-only-session-inspection) during a hold. Then look at locks held by non-idle sessions:

```sh
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d airline_booking_demo'
```

```sql
SELECT l.locktype, l.mode, l.granted, count(*)
FROM pg_locks l JOIN pg_stat_activity a USING (pid)
WHERE a.datname = current_database() AND a.pid <> pg_backend_pid() AND a.state <> 'idle'
GROUP BY 1, 2, 3 ORDER BY 1, 2;
```

Record `state`, `wait_event_type`, `wait_event`, `pg_blocking_pids`, the transaction age, and the first part of the query text.

**With Prometheus**, graph these over the same window (full set in [observability](../observability.md#promql-for-both-incidents)):

```promql
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{
  job="mac-go-api",route="POST /v1/bookings/{booking_id}/seat-holds",status="200"}[5m])))
```

```promql
up{job="mac-go-api"}
```

A 15-second scrape can land between two 2-second holds, so a `db_pool_acquired_connections` of 0 on a graph doesn't mean the pool was unused. Counters like `db_pool_acquires_total` keep the history that gauges lose.

## 5. What we know vs what we assume

| Known (measured) | Assumed or unknown so far |
| --- | --- |
| Holds take ~1–2.5 s and succeed | That this is "normal database slowness" |
| Health, readiness, and `up` all look fine | That the pool is exhausted (one connection was in use) |
| Network connect time is negligible | That a row lock is blocking the hold |
| Server-side duration matches client duration | That an external service is involved |
| API and PostgreSQL CPU are near idle | That adding CPU or pool connections would help |
| Other endpoints on the same booking are fast | Which statement inside the hold is slow |

Low CPU plus high latency means *waiting*. The rest of the investigation is about finding out what the request waits on.

## 6. Narrow the problem

Work through the candidates using the evidence from step 4:

1. **Network?** No. Connect time is under a millisecond, and server duration equals client duration.
2. **CPU-heavy code?** No. Both processes are idle during the request.
3. **Pool exhaustion?** Not at this load. One connection was acquired out of 20. (Under heavy concurrency it could become a *consequence*. See the lesson.)
4. **Lock contention?** **Lab observation:** the only locks held were 13 `AccessShareLock` relation locks (ordinary reads) and one `virtualxid` lock, all granted, and `pg_blocking_pids` was empty. A blocked hold would show an ungranted lock and a non-empty blocker list.
5. **A slow statement?** **Lab observation:** there was one `active` session, in a transaction open for ~0.85 s at sample time, with `wait_event_type = Timeout` and `wait_event = PgSleep`, running a `SELECT count(*) … FROM (SELECT pg_sleep($2)) …` over `flight_seats` and `seat_assignments`.

That last row settles it. `PgSleep` is the wait event PostgreSQL reports when a statement calls `pg_sleep()`. The database isn't struggling. It was asked to wait, while the application's transaction and pool connection stay open.

The question left: *why does the seat-hold code send a query that sleeps?*

## 7. Root cause

**Code fact:** [`HoldSeats`](../../internal/booking/hold_seats.go) begins a READ COMMITTED transaction, which checks out a pool connection. Before the idempotency check and before any row locks, it calls [`reconcileHeldInventory`](../../internal/booking/inventory_reconcile.go). With `LAB_FAULTS` containing `booking-latency`, that function picks a random delay between 1 and 3 seconds and runs:

```sql
SELECT count(*)
FROM (SELECT pg_sleep($2)) AS settle,
     flight_seats AS fs
     LEFT JOIN seat_assignments AS sa
       ON sa.flight_seat_id = fs.id AND sa.status = 'HELD'
WHERE fs.flight_instance_id = $1
  AND fs.state = 'HELD';
```

The result is scanned and thrown away. Despite the name and comment, nothing is reconciled or validated. Afterwards the normal hold logic runs unchanged: locks, availability checks, writes, commit.

That explains every observation:

- **Latency without CPU:** PostgreSQL is sleeping, not working.
- **Variable latency:** the delay is random.
- **Health checks green:** `/healthz` and `/readyz` never run this code.
- **No lock waits:** the sleep happens before the hold takes its row locks.
- **Other booking endpoints fast:** only `HoldSeats` calls it.
- **Correct results:** the real hold logic is untouched.

**Lab observation** from checking the query directly in `psql`: with a 0.5 s sleep argument, it took ~0.5 s both for a flight with held seats and for one without. The sleep ran either way on this data.

## 8. Verify

```sh
LAB_FAULTS= docker compose --profile app up -d --force-recreate api
curl --fail-with-body "$B/readyz"
```

The access token is still valid if under 10 minutes have passed, so rerun the single hold from step 2 and the endpoint comparison from step 3. **Lab observation:** with the fault off, the same hold returned 200 in ~0.024 s. It's a bit slower than a GET because it takes locks and writes rows, but it's nowhere near seconds. There should be no `PgSleep` session in `pg_stat_activity` during a hold. In Prometheus, compare seat-hold p95 over a fresh window.

Release the seat when you're done:

```sh
curl -sS -o /dev/null -w 'release status=%{http_code}\n' -X DELETE "$B/v1/bookings/$BOOKING_ID/seat-holds" \
  -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: $(uuidgen)"
unset TOKEN HOLD_BODY
```

Holds you don't release expire after 15 minutes, and the worker cleans them up.

## 9. Lesson

- **UP isn't fast.** Every health signal was green during the whole incident. Only a latency measurement on the business route showed the problem.
- **Low CPU plus high latency means waiting.** Find *what* the request waits on: network, pool, lock, statement, or timer. Don't just add capacity.
- **`pg_stat_activity` answers "what is this connection doing right now?"** `wait_event` separates a lock wait (`Lock`) from I/O or a deliberate sleep (`Timeout/PgSleep`).
- **Application behavior can look like a database problem.** PostgreSQL was healthy. The application asked it to wait while holding a transaction open.
- **Bigger pools don't fix slow transactions.** Every hold keeps a connection checked out for 1–3 s. At about 20 concurrent holds, the API's pool (max 20) runs out, and *unrelated* routes start waiting for connections. Raising the pool size only lets more requests wait at once. (Not measured in this write-up. Try it and watch `db_pool_empty_acquires_total`.)

## Generating steady traffic

This loop prepares one booking and re-holds it every ten seconds for four minutes. It stays under the 10-minute token lifetime and the rate limits (30 auth requests per IP per minute, 60 booking mutations per user per minute), and it releases the seat at the end:

```sh
(
  set -e
  out=$(B="$B" DATE="$DATE" sh scripts/lab-booking-setup.sh); eval "$out"
  for i in $(seq 1 24); do
    curl -sS -o /dev/null -X POST "$B/v1/bookings/$BOOKING_ID/seat-holds" \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -H "Idempotency-Key: $(uuidgen)" --data "$HOLD_BODY" \
      -w "hold $i status=%{http_code} total=%{time_total}s\n"
    sleep 10
  done
  curl -sS -o /dev/null -X DELETE "$B/v1/bookings/$BOOKING_ID/seat-holds" \
    -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: $(uuidgen)" -w 'release status=%{http_code}\n'
)
```

A 401 means the token expired. A 409 means the seat went to someone else or the hold expired. A 429 means you hit a rate limit. Stop and look at any of these. None of them is a fast successful hold.
