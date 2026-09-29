# Documentation verification notes

[Back to the lab](../README.md)

> **Spoilers:** this page names the injected faults directly. Finish the labs first.

This repository mixes a current local troubleshooting lab with older delivery/audit documents. These notes mark boundaries that matter while following the incident guides.

- The active health routes are `/healthz` and `/readyz`. `/metrics` is implemented in `cmd/api` and `internal/observability/prometheus.go`. Earlier security/delivery text that says an exporter still needs wiring predates this local lab. Distributed tracing is still an interface only, without spans or a backend.
- `LAB_FAULTS=cpu` stretches a search-result ETag with SHA-256 rounds. `LAB_FAULTS=latency` waits on flight details. `LAB_FAULTS=booking-latency` sends `pg_sleep` as part of a PostgreSQL query in the seat-hold transaction. Neither the booking reconciliation's comment nor its returned `held` count implies an actual repair/validation step; the count is unused.
- The booking fault's `pg_sleep` ran both for a flight with held seats and for one without when the query was tested directly in `psql` against the seeded data (about 0.5 s each for a 0.5 s argument). An earlier draft said the planner could skip it on flights with no holds; that wasn't observed and has been removed.
- The API binds to `API_BIND_ADDR`. If that is a Tailscale address and Tailscale is stopped, `docker compose up` fails with `bind: can't assign requested address`; this was observed during the documentation pass.
- The current local Compose setup is the Mac lab: PostgreSQL on localhost:5433 and API bound to `API_BIND_ADDR` (localhost by default). The existing [AWS application deployment](aws-deployment.md) is a separate pipeline/host and is not the Ubuntu k3s monitoring example. Its environment-specific account/instance/profile identifiers should not be copied as general instructions.
- The repo has no checked-in k3s cluster state, Prometheus/Grafana dashboard exports, Tailscale policy, or saved CPU/latency experiment data. The monitoring page is a reproducible example based on the reported topology. Measured CPU/p95 values in a fresh run depend on hardware, traffic, and scrape timing.
- Existing docs include historical [changed-files](changed-files.md) and [delivery](delivery.md) reports. They describe an earlier implementation pass and are retained as provenance, not as a live status checklist.

Checks for this documentation pass are reported in the final response. Any command requiring the user's EC2 cluster, Tailscale account, or external package installation remains an example until run in that environment.
