# Lab 1: CPU incident

[Back to the lab](../../README.md) · [Metrics and PromQL](../observability.md) · [Commands](../commands.md)

Work through the sections in order. The cause is near the end on purpose.

Three kinds of statements appear below, and they're labeled:

- **Code fact:** something you can read in this repository.
- **Lab observation:** a measurement from one run on one machine. Yours will differ.
- **Lesson:** general troubleshooting practice.

## 1. Symptom

> "Flight search got slow after the last deploy, and the API hosts are running hot."

Nobody has said which requests, how slow, or since when. That's normal.

## 2. Reproduce

Finish [local setup](../running.md) first. Then enable the scenario and build a valid query:

```sh
export B=${B:-http://127.0.0.1:8080}
export DATE=$(date -u -v+1d +%F 2>/dev/null || date -u -d tomorrow +%F)
export Q="origin=CAI&destination=JED&date=$DATE&limit=20"

LAB_FAULTS=cpu docker compose --profile app up -d --force-recreate api
curl --fail-with-body "$B/readyz"               # retry until it returns ready
docker compose logs api | grep lab_faults       # should show ["cpu"]
curl --fail-with-body -i "$B/v1/flights?$Q"
```

You want `HTTP/1.1 200` and an `items` array. The seeder creates two CAI→JED flights per day, so expect two items.

### The fast load test that was all 400s

**Lab observation:** the first load test in the original experiment reported high throughput and low latency. Every single response was HTTP 400.

Try the request that test sent:

```sh
curl -i "$B/v1/flights"
```

**Code fact:** `GET /v1/flights` validates its query before touching the database (`internal/flight/store.go`):

| Parameter | Requirement |
| --- | --- |
| `origin`, `destination` | Three-letter airport codes, and they must differ |
| `date` | `YYYY-MM-DD`, treated as a UTC day |
| `limit` | Optional, 1–100, default 50 |
| `after_id` + `after_departure` | Optional pagination cursor. Both are required together. |

A rejected request returns `400 invalid_input` without running SQL or anything after it. A benchmark of 400s measures the validation path, not search.

**Lesson:** before you trust any throughput number, look at the status-code breakdown and one response body.

## 3. Measure

Take a small sequential sample, then light parallel load:

```sh
for i in 1 2 3 4 5; do
  curl -sS -o /dev/null \
    -w 'status=%{http_code} connect=%{time_connect}s ttfb=%{time_starttransfer}s total=%{time_total}s\n' \
    "$B/v1/flights?$Q"
done

hey -z 15s -c 4 "$B/v1/flights?$Q"      # optional; check the status distribution at the bottom
```

While `hey` runs, in another terminal:

```sh
docker stats --no-stream go-airline-booking-api-1 airline-postgres
```

Then run the same commands with the fault off (`LAB_FAULTS=`) to get a baseline. Always compare against something.

**Lab observation** (MacBook with 8 CPUs, one run):

| | Fault off | `LAB_FAULTS=cpu` |
| --- | --- | --- |
| Single valid search, total | ~0.002–0.003 s | ~0.32 s |
| `hey -c 4`, 15 s | ~4,870 req/s, all 200 | ~11 req/s, all 200 |
| API container CPU under load | ~75% | ~397% |
| PostgreSQL CPU under load | ~53% | ~0.4% |
| API memory | ~13 MiB | ~16 MiB |

`docker stats` reports CPU per core: 100% is one full core, and 400% is four. With `-c 4`, 397% means about four cores were busy the whole time.

## 4. Gather evidence

**Logs:** each request writes a JSON access line with `request_id`, `status`, and `duration_ms`:

```sh
docker compose logs --since 5m api | tail -5
```

A server-side `duration_ms` close to curl's total rules out the network as the main cost.

**Metrics:** if Prometheus scrapes the API (see [monitoring](../monitoring/prometheus-grafana.md)), graph these over the same window:

```promql
rate(process_cpu_seconds_total{job="mac-go-api"}[1m])
```

```promql
sum by (status) (rate(http_requests_total{job="mac-go-api",route="GET /v1/flights"}[1m]))
```

```promql
histogram_quantile(0.95, sum by (le) (
  rate(http_request_duration_seconds_bucket{job="mac-go-api",route="GET /v1/flights",status="200"}[5m])))
```

```promql
rate(db_pool_acquire_duration_seconds_total{job="mac-go-api"}[1m])
```

Without Prometheus, `curl -s "$B/metrics" | grep -E '^(process_cpu|db_pool_acquired|go_goroutines)'` gives the raw values.

**Database:** during load, look at what PostgreSQL is doing with the activity query in [database troubleshooting](../database-troubleshooting.md#read-only-session-inspection).

## 5. What we know vs what we assume

| Known (measured) | Assumed or unknown so far |
| --- | --- |
| Valid searches are ~100× slower than baseline | That the search SQL is the slow part |
| API CPU scales with concurrent valid searches | That more CPU would fix it |
| PostgreSQL CPU is near idle during the slowdown | That this is a "database problem" |
| Invalid searches are still fast | That every route is affected |
| Memory barely moves | Anything about GC without looking at GC metrics |

The tempting guess is "search queries got slow, add an index." Nothing measured so far supports it.

## 6. Narrow the problem

Ask questions that split the request path in half:

1. **Is it the database?** PostgreSQL CPU is flat. **Lab observation:** over 10 s of `hey -c 4`, `db_pool_acquires_total` rose by 117 while `db_pool_acquire_duration_seconds_total` rose by only ~0.01 s, so requests weren't waiting for connections either. Whatever burns CPU is inside the API process.
2. **Is it proportional to data?** Search a date with no flights:

   ```sh
   curl -sS -o /dev/null -w 'status=%{http_code} total=%{time_total}s\n' \
     "$B/v1/flights?origin=CAI&destination=JED&date=2030-01-01"
   ```

   **Lab observation:** an empty result took ~0.29 s, almost as slow as a two-item page. So the cost isn't driven by the rows returned.
3. **Before or after validation?** Invalid requests stay fast (~1–3 ms), so the expensive part runs after validation. With the empty-page result, it most likely runs after the query too, while the response is being built.
4. **Is it only this route?** `GET /v1/flight-instances/{id}` uses the same store and database and stays fast.

Now the question is small: *what does the search handler do after a successful query, before writing the response?* This build has no `pprof` endpoint. In a real service, a CPU profile would answer that directly. Here you read the handler.

## 7. Root cause

**Code fact:** the handler in [internal/httpapi/flight.go](../../internal/httpapi/flight.go) calls `searchETag` on the result page before writing the JSON. With `LAB_FAULTS` containing `cpu`, [`searchETag`](../../internal/httpapi/flight_integrity.go):

1. JSON-encodes each result and runs **60,000 SHA-256 rounds per item**.
2. Then runs another **2,400,000 rounds** (`60,000 × 40`), even for an empty page.
3. Sets the result as an `ETag` header.

The work is pure CPU, never checks the request context, and uses no database. That matches every observation: CPU scales with concurrency, PostgreSQL is idle, empty pages are still slow, and invalid requests skip it. Requests cut off by the 10-second `http.TimeoutHandler` keep computing in the background until the loop finishes.

The code comment frames this as "hardening" the ETag. It's the injected fault, not a real caching or security technique.

## 8. Verify

```sh
LAB_FAULTS= docker compose --profile app up -d --force-recreate api
curl --fail-with-body "$B/readyz"
curl -sS -o /dev/null -w 'status=%{http_code} total=%{time_total}s\n' "$B/v1/flights?$Q"
hey -z 15s -c 4 "$B/v1/flights?$Q"
```

Compare with the numbers from step 3, using the same concurrency and query. For Prometheus graphs, use a fresh time window, because 5-minute rates still contain fault-era samples. Confirm the response no longer carries an `ETag` header (`curl -sI "$B/v1/flights?$Q"`), since that header only exists when the fault is on.

## 9. Lesson

- High CPU with an idle database points into the application process. Confirm it with the empty-result and invalid-request comparisons.
- A benchmark is only as good as its status codes.
- Work that ignores the request context keeps using CPU after the client has given up, so timeouts don't protect you from it.
- You didn't need to know Go to solve this. You needed to know that a successful request takes a different path than a rejected one, and to test each half of that path.
