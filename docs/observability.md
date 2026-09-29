# Observability: what the numbers can tell you

[Back to the lab](../README.md) · [Booking incident](labs/booking-latency.md)

The implementation is in [prometheus.go](../internal/observability/prometheus.go), wired in [cmd/api](../cmd/api/main.go). `/metrics` is served before the API middleware and is not counted in the HTTP instruments. Health requests are counted; the browser assets share `route="webui"`, and unknown routes use `route="unmatched"`.

The application emits `method`, `route`, and `status` labels. Prometheus adds scrape labels such as `job` and `instance`. The examples below assume the [monitoring guide](monitoring/prometheus-grafana.md) sets `job="mac-go-api"`; that label does not appear in a raw application scrape.

## Metrics implemented here

| Metric | Type | Incident use and limit |
| --- | --- | --- |
| `http_requests_total` | Counter | Completed requests by method, route, status. Use rates; a lifetime total is not current traffic. |
| `http_request_duration_seconds` | Histogram | Completed HTTP duration, including middleware and business logic. It does not separate SQL from application time or include client network transit. |
| `http_requests_in_flight` | Gauge | Requests being served. A rising value can mean more traffic or longer waits. |
| `process_cpu_seconds_total` | Counter | CPU seconds for the API process. Its rate is CPU core-equivalents, not a percentage of the whole machine. |
| `process_resident_memory_bytes` | Gauge | Resident process memory; compare with limits and time. It is not just the Go heap. |
| `go_goroutines` | Gauge | Existing goroutines. Waiting requests can increase it; a count alone does not establish a leak. |
| `go_memstats_heap_alloc_bytes`, `go_memstats_heap_inuse_bytes` | Gauges | Heap allocation/use; compare trends with RSS and request load. |
| `go_memstats_alloc_bytes_total` | Counter | Allocation churn even when live memory stays small. |
| `go_gc_duration_seconds` | Summary | GC pause observations, sum, and count. Its quantiles are not HTTP histogram buckets and cannot be averaged across processes. |
| `go_gc_gogc_percent`, `go_gc_gomemlimit_bytes`, `go_info` | Gauges/info | Runtime settings and version context. Defaults are not evidence of memory pressure. |

The registered Go/process collectors provide additional version/platform-dependent series. Linux containers expose process CPU/RSS; a host Go process on macOS may not expose the same process collector series. Check actual `/metrics` output before diagnosing missing graphs.

### Database pool metrics

These are **the API process's pgx pool**, not PostgreSQL's global connection count and not the worker's separate pool. Pool statistics are read at scrape time.

| Metric | Type | Meaning |
| --- | --- | --- |
| `db_pool_acquired_connections` | Gauge | Connections currently checked out, including waiting SQL/transactions |
| `db_pool_idle_connections` | Gauge | Connections immediately available in the pool |
| `db_pool_total_connections` | Gauge | Idle + acquired + constructing connections |
| `db_pool_constructing_connections` | Gauge | Connections being established |
| `db_pool_max_connections` | Gauge | Configured upper bound (20 by default) |
| `db_pool_acquires_total` | Counter | Successful acquisitions |
| `db_pool_empty_acquires_total` | Counter | Successful acquisitions that waited because no idle connection was available |
| `db_pool_canceled_acquires_total` | Counter | Acquisitions canceled by their context |
| `db_pool_acquire_duration_seconds_total` | Counter | Cumulative time spent successfully acquiring connections; not SQL execution duration |

A growing acquisition duration plus empty acquisitions and acquired connections near the maximum supports pool contention. It does not identify why connections stay busy. Inspect sessions and transaction lifetimes next.

`db_pool_acquired_connections=0` at one scrape does **not** prove the database was never involved. With a 15-second scrape interval, a two-second query can begin and finish between scrapes. Gauges miss that activity; cumulative counters can retain evidence. Scrape more frequently for a controlled exercise or inspect PostgreSQL while a request is running.

## Route labels and cardinality

```text
GOOD: POST /v1/bookings/{booking_id}/seat-holds
BAD:  POST /v1/bookings/83918391/seat-holds
```

Each distinct label combination creates a time series. IDs multiply that count continuously, and histograms multiply it again by bucket count. The code uses Go router patterns, including the HTTP method in the `route` value, to keep series bounded. Request IDs belong in logs, not metric labels.

## PromQL for both incidents

Paste each expression separately into Prometheus or Grafana Explore. Use the same time range for all panels.

### Scrape availability, traffic, and errors

```promql
up{job="mac-go-api"}
```

One means the last scrape succeeded; zero means it failed. No series means the target may not be discovered or your labels/time range may be wrong. Neither a successful scrape nor a request counter proves all business routes work.

```promql
http_requests_total{job="mac-go-api"}
```

```promql
sum by (route) (
  rate(http_requests_total{job="mac-go-api"}[1m])
)
```

Always look at status codes alongside rate:

```promql
sum by (route, status) (
  rate(http_requests_total{job="mac-go-api"}[1m])
)
```

For server-error fraction (0–1), with no traffic producing an undefined result:

```promql
sum(rate(http_requests_total{job="mac-go-api",status=~"5.."}[5m]))
/
sum(rate(http_requests_total{job="mac-go-api"}[5m]))
```

Also inspect 400 (invalid request), 409 (conflict), and 429 (rate limit); they change how a load test should be interpreted.

### p95 and average latency

All routes, across statuses:

```promql
histogram_quantile(
  0.95,
  sum by (le, route) (
    rate(http_request_duration_seconds_bucket{job="mac-go-api"}[5m])
  )
)
```

Successful seat holds only:

```promql
histogram_quantile(
  0.95,
  sum by (le, route) (
    rate(http_request_duration_seconds_bucket{
      job="mac-go-api",
      method="POST",
      route="POST /v1/bookings/{booking_id}/seat-holds",
      status="200"
    }[5m])
  )
)
```

Successful flight search: use the same expression with `method="GET"` and `route="GET /v1/flights"`. Replace `0.95` with `0.99` for p99, but expect unstable estimates with very little traffic.

Average successful hold duration in seconds:

```promql
sum by (route) (
  rate(http_request_duration_seconds_sum{
    job="mac-go-api",route="POST /v1/bookings/{booking_id}/seat-holds",status="200"
  }[5m])
)
/
sum by (route) (
  rate(http_request_duration_seconds_count{
    job="mac-go-api",route="POST /v1/bookings/{booking_id}/seat-holds",status="200"
  }[5m])
)
```

The histogram has boundaries at 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, and 10 seconds, plus `+Inf`. Buckets are cumulative: `le="5"` includes requests completed within five seconds. Preserve `le` when aggregating so Prometheus can interpolate a percentile.

p95 estimates the duration below which 95% of observed requests fall. It is not the slowest request and is not exact. For example, a reported 4.5-second p95 can reflect interpolation in the wide 2.5–5-second bucket; it does not prove an individual request took 4.5 seconds.

Initially you may see no series or `NaN`: the label combination may not exist yet, `rate` may have fewer than two scrape samples, or there may be no new observations in the selected window. Generate valid traffic across several scrapes. A longer window helps with sparse traffic but mixes more of the earlier baseline/fault state. Do not replace missing values with zero and call the service fast.

### CPU, goroutines, memory, and in-flight requests

```promql
rate(process_cpu_seconds_total{job="mac-go-api"}[1m])
```

```promql
go_goroutines{job="mac-go-api"}
```

```promql
process_resident_memory_bytes{job="mac-go-api"}
```

```promql
http_requests_in_flight{job="mac-go-api"}
```

CPU rate 2 means roughly two cores over the window. Low CPU with high duration suggests waiting; it does not identify the dependency. Small RSS does not rule out all memory/GC issues. Compare heap and GC trends, load, and available resources.

### Pool activity and acquisition cost

```promql
db_pool_acquired_connections{job="mac-go-api"}
```

```promql
db_pool_idle_connections{job="mac-go-api"}
```

```promql
db_pool_total_connections{job="mac-go-api"}
```

```promql
rate(db_pool_acquire_duration_seconds_total{job="mac-go-api"}[1m])
```

That last expression is accumulated acquisition seconds per wall-clock second, not average query latency. Compare with these expressions separately:

```promql
rate(db_pool_empty_acquires_total{job="mac-go-api"}[1m])
```

```promql
rate(db_pool_canceled_acquires_total{job="mac-go-api"}[1m])
```

Mean time per successful acquisition:

```promql
rate(db_pool_acquire_duration_seconds_total{job="mac-go-api"}[5m])
/
rate(db_pool_acquires_total{job="mac-go-api"}[5m])
```

A low acquisition time is compatible with a slow SQL query after acquisition. A high value may include connection construction as well as waiting for a free connection.

## Logs and the tracing boundary

API JSON access logs contain `request_id`, `status`, and `duration_ms`. Match the response's generated `X-Request-ID` to its log line. They deliberately omit raw paths, query strings, headers, bodies, and SQL error details. A long access-log duration confirms server-side time, not the stage responsible for it.

`internal/observability/observability.go` defines a `Tracer` interface, but no adapter, spans, trace propagation, or tracing backend is wired in. There are no SQL timing spans to inspect in this lab. Use request logs, metrics, PostgreSQL activity, and source inspection together. In a traced service, spans around pool acquisition, SQL execution, and downstream calls would make those boundaries visible; that is a future extension, not existing evidence.
