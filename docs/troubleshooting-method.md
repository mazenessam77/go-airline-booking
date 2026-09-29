# Investigate before guessing

[Back to the lab](../README.md)

An incident report says what hurts. Your job is to turn it into a question you can answer with evidence: which requests, since when, how often, and where does the time go?

```text
Symptom → Reproduce → Measure → Gather evidence → Known vs assumed
        → Narrow the problem → Root cause → Verify → Lesson
```

Both labs follow this order. The steps below say what each one means in practice.

1. **Scope the symptom.** Search, flight details, or seat holds? All users or one booking? Record the time window and fault setting.
2. **Reproduce.** Inspect the response body and status. A 400 response exercises a different path from a successful booking.
3. **Measure.** Capture connection time, TTFB, total time, and request ID. Compare the client measurement with the access log.
4. **Check resources.** Compare API and PostgreSQL CPU; look at RSS, heap/GC, goroutines, and in-flight requests over the same period.
5. **Check dependencies.** Use pool counters and gauges, then active PostgreSQL sessions, wait events, and blockers. Do not infer “no database use” from one idle scrape.
6. **Compare paths.** A fast booking GET beside a slow hold POST narrows the investigation, but does not rule out a query-specific database issue.
7. **Write a hypothesis with a disproof.** For example: “Requests queue for pool connections. If that's true, acquired connections sit at the maximum and `db_pool_empty_acquires_total` climbs during the slowdown.” If the prediction fails, the hypothesis is wrong or the sample is too small.
8. **Confirm and verify.** Inspect the relevant source after narrowing the layer. Disable the fault, recreate the API, and repeat the same valid requests at the same concurrency. Watch both errors and latency.

## What we know vs what we assume

Example during the booking incident:

| Known, after measuring | Not yet known |
| --- | --- |
| The request latency is high | Whether PostgreSQL execution is slow |
| The API is reachable | Whether the pool is exhausted |
| Prometheus reports the target UP | Whether a row/advisory lock blocks progress |
| API CPU is low | Whether application code is sleeping or blocking |
| A health request succeeds | Whether an external dependency is slow |

Low CPU narrows one direction. It does not establish a root cause. A process can wait on SQL, disk, a mutex, a timer, a connection, or a network response.

## Keep a small incident record

Record the request's route pattern, status, request ID, timestamps, fault setting, concurrency, timing, and relevant graphs. Do not save tokens or passenger details. Separate:

- **Code facts:** the function and transaction order you inspected.
- **Observations:** what your run actually returned or measured.
- **Interpretation:** the hypothesis those observations support.

After a change, compare a fresh observation window. Counters reset on restart, old samples remain in a five-minute rate window, and a sparse p95 may be misleading. A fix is verified when the user path improves under comparable traffic without new failures.
