# Incident command sheet

[Back to the lab](../README.md)

These commands use the local lab and fictional data. Set `B=http://127.0.0.1:8080` for the default API binding, or use your own Tailscale address when configured. `POST /seat-holds` needs the authenticated setup in [Lab 2](labs/booking-latency.md).

## Docker

```sh
docker compose ps
docker stats
docker compose logs --since 5m --tail 100 api
docker compose logs --since 5m --tail 100 postgres
docker compose logs api | grep lab_faults       # active faults, from the startup log
api_id=$(docker compose --profile app ps -q api)
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$api_id" | grep '^LAB_FAULTS='
```

Filter `docker inspect` output down to the line you need. The full container environment includes the database URL and password.

## Linux and macOS process checks

```sh
ss -lntp                         # Linux: listening TCP sockets
lsof -nP -iTCP:8080 -sTCP:LISTEN # macOS/Linux: port 8080 listener
top                               # interactive CPU/memory view
```

On macOS, `ss` is usually unavailable; use `lsof`. Container CPU is often easier to compare with `docker stats`.

## HTTP

```sh
curl -i "$B/healthz"
curl --fail-with-body -i "$B/readyz"
curl --fail-with-body -sS -o /dev/null \
  -w 'status=%{http_code} connect=%{time_connect}s ttfb=%{time_starttransfer}s total=%{time_total}s\n' \
  "$B/v1/flights?$Q"
```

Set `Q` with valid origin, destination, and date as in [Lab 1](labs/cpu.md). Use `-i` when inspecting status, response body, and `X-Request-ID`.

## PostgreSQL

```sh
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d airline_booking_demo'
```

In `psql`, use the read-only activity query in [database troubleshooting](database-troubleshooting.md#read-only-session-inspection). `\watch 1` samples while a request is running; `\q` exits.

## Prometheus and Grafana Explore

```promql
up{job="mac-go-api"}
```

```promql
sum by (route, status) (rate(http_requests_total{job="mac-go-api"}[1m]))
```

See [p95, CPU, memory, and pool queries](observability.md#promql-for-both-incidents).

## Kubernetes

```sh
kubectl -n monitoring get pods
kubectl -n monitoring get servicemonitor
kubectl -n monitoring get endpoints mac-go-api
kubectl -n monitoring get prometheus
```

Use these only after configuring your own [monitoring cluster](monitoring/prometheus-grafana.md). The `Endpoints` command matches the example manifest.

## Networking

```sh
lsof -nP -iTCP:8080 -sTCP:LISTEN
curl -v --connect-timeout 5 http://YOUR_MAC_TAILSCALE_IP:8080/metrics
```

From EC2, substitute your own private Tailscale IP. If host `curl` works but the Prometheus target is DOWN, inspect Kubernetes discovery, pod networking, and Tailscale reachability from the scraper's namespace.
