# Delivery and audit report

## Initial findings

The starting branch was main. The pre-existing untracked seat-race integration test
was preserved and extended. No commit, push or merge was made. The three original
migrations were already applied and were not edited.

The critical ownership check was missing: HoldSeatsParams had no UserID and the
booking lock query did not filter by owner. This is corrected with a validated
UserID and `AND b.user_id = $4`. HTTP handlers derive it from verified authentication
context, and strict request DTOs reject client-supplied user IDs.

Other findings included transaction-start timestamps becoming stale during lock waits,
incomplete inventory-expiry consistency checks, acceptance of the nil UUID, and missing
fare-cabin/aircraft eligibility checks. These were corrected. The initial Go 1.26.2
standard library had ten reachable vulnerability findings; the project and Dockerfile
now require the patched Go 1.26.8 release.

## Implemented scope

- Ownership-safe seat holds, deterministic inventory locking, atomic rollback,
  repeated requests, expiry reclamation and bounded concurrent expiration workers.
- Secure HTTP middleware, strict JSON, generated request IDs, redacted UTC logs,
  timeouts, exact CORS allowlists and explicitly trusted proxy handling.
- Argon2id authentication, hashed opaque credentials, rotation/reuse detection,
  logout/logout-all, role checks and PostgreSQL-backed authentication rate limits.
- Bounded flight search, flight details with active fare offers, private seat maps,
  server-priced quotes, single-consumption bookings and draft passenger editing.
- Owner-scoped booking/seat endpoints and atomic payload-bound idempotency records.
- Provider-independent payment/refund services, signed-webhook verification,
  duplicate/out-of-order event handling, late-success refund requests and fake-provider tests.
- Transactional outbox with leases, retry scheduling, dead letters and consumer receipts.
- Booking lifecycle checks in services and PostgreSQL, unpaid cancellation, paid
  refundable cancellation/refund orchestration, and a ticketing integration boundary.
- Separate owner/runtime role scripts, restricted runtime grants, TLS validation,
  retention/rotation/backup guidance, and an explicit RLS evaluation.
- OpenAPI 3.1, ADRs, ERD, Dockerfile, Compose profiles/health checks, Makefile,
  CI, benchmarks, query-plan diagnostics, observability interfaces and smoke scripts.
- OIDC-based GitHub-to-AWS demo delivery, private immutable ECR releases, and a
  single-instance SSM deployment that leaves the existing Apache service unchanged.
- Embedded responsive web UI for flight search, trusted fares, authentication, owned
  bookings, fictional passengers and atomic seat holds, backed by an idempotent isolated
  demo-data seeder and a dependency-free end-to-end smoke journey.

## Verification and commands

Commands used during delivery:

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./internal/booking/...
go test -race ./...
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
GOOSE_BIN=/Users/macbookair/go/bin/goose CHECK_MIGRATION_ROUNDTRIP=1 sh scripts/test-local.sh
GOOSE_BIN=/Users/macbookair/go/bin/goose RUN_BENCHMARKS=1 RUN_QUERY_PLANS=1 CHECK_RUNTIME_GRANTS=1 sh scripts/test-local.sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run golang.org/x/vuln/cmd/govulncheck@latest -show verbose ./...
npx --yes @redocly/cli@latest lint api/openapi.json
docker build -t airline-booking:audit .
sh scripts/smoke-local.sh
GOOSE_BIN=/Users/macbookair/go/bin/goose sh scripts/demo-local.sh
node scripts/ui-smoke.mjs
git diff --check
```

Unit/integration/race checks passed during the implementation. PostgreSQL tests use
only TEST_DATABASE_URL and verify the actual database name before cleanup. The
dedicated airline_booking_codex_test database was created for this work. Migration
up/down-to-zero/up was verified there. Runtime privileges were checked in a transaction
that rolled back both role creation and grants. No production grants were applied.

The Docker image built. API and worker binaries started against the test database;
health/readiness returned 200, and both processes stopped on SIGTERM. Go vulnerability
scanning reports zero reachable or imported-package findings. It still notes the
unmaintained, unused golang.org/x/crypto/openpgp package at module level; this application
imports Argon2id and does not import OpenPGP. OpenAPI validation passes with one warning
because the repository has no chosen license; a legal license was not invented.

## Remaining gates and recommended next work

Real payment/refund dispatch and webhook ingestion remain disabled in production wiring.
They require explicit authorization, credentials and a reviewed provider adapter;
the fake provider is used only by tests. Ticket issuance and actual customer delivery
also require their respective external integrations. The default outbox consumer
records durable domain-ledger receipts and does not send notifications.

Before public launch, approve jurisdiction-specific PII retention/legal-hold rules,
complete account verification/reset/MFA design, provision production database roles and
TLS secrets, wire telemetry exporters and the ingress, and test backup restore and
representative multi-replica load. RLS is intentionally deferred until pooled
transaction-scoped identity has a tested design. OWASP coverage is not certification;
an independent deployment/security review remains necessary.

The original application database was not migrated. Apply the new migrations with the
migration owner after reviewing them, then start API and worker using runtime credentials.
The local test database, compiled binaries and audit Docker image remain available;
the smoke-test processes were stopped. No secrets or local .env files were staged or committed.

See [changed files](changed-files.md) for the complete source/documentation inventory.
