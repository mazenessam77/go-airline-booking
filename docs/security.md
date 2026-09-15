# Security and operations

This implementation is not an OWASP certification or a completed production security review.

## Database access

Provision separate migration and runtime logins. Run ops/roles.sql as an administrator,
run migrations with SET ROLE airline_owner, then ops/grants.sql as that owner. Use the
runtime login in API and worker secrets. Never run migrations from application startup.
The checked-in Compose database is a development environment, not the production role model.
Runtime must not own tables, CREATE objects, change roles, or access goose_db_version.
Revoke PUBLIC CONNECT on the target database with an administrator and grant CONNECT
only to the required logins. Review PUBLIC function execution and extensions separately.

APP_ENV=production requires an explicit PostgreSQL URL with sslmode=verify-full and
sslrootcert. Terminate HTTPS at a trusted ingress. Set TRUSTED_PROXY_CIDRS to that
ingress's exact ranges; never use 0.0.0.0/0. CORS is disabled by default and only exact
configured origins are allowed; credentials cookies are not enabled. Deploy connection
limits, ingress rate limits, request limits and TLS settings alongside the application.

RLS is deferred. Object ownership is explicit in service queries. A future RLS design
must SET LOCAL user context in every transaction and prove pool cleanup, worker access,
and administrative behavior. It must remain defense in depth.

## Logging and telemetry

Access logs contain generated request IDs, status and elapsed time. Never log raw
paths, query strings, Authorization, Cookie, passwords, tokens, PNRs, email addresses,
passenger details, travel documents, payment bodies, PostgreSQL errors or panic values.
Startup and worker logs use fixed error codes. Diagnostic details belong in a separately
restricted, redacted operational workflow. Metrics and trace labels must have bounded
cardinality and exclude customer identifiers. The observability package supplies interfaces
and atomic counters; an exporter and distributed tracing backend still need deployment wiring.

## Rotation, backup and retention

Rotate database credentials by provisioning a new runtime login with identical grants,
updating secret references, rolling API/worker instances, validating readiness, then
revoking the old login. Never place credentials in Git or command transcripts.
Refresh credentials are revoked through logout-all or session revocation. Database
backups contain password/token hashes and PII and require encryption and restricted access.

Use pg_dump custom-format logical backups plus managed WAL archiving/PITR. Restore
into an isolated database, apply missing migrations with the owner role, verify row counts,
constraints and booking/seat consistency, then run a recovery exercise before cutover.
Set and test RPO/RTO with the deployment operator; do not assume that a successful backup
means restore works. Never restore over the live database without a reviewed recovery plan.

ops/retention.sql supplies bounded cleanup for rate buckets, HTTP replay records,
expired sessions and audit records. Defaults are one day for replay/session cleanup
and 90 days for security audits. Refresh reuse evidence remains until the session expires.
Passenger PII, travel documents, payments, refunds and webhook evidence need an approved
retention period, legal-hold checks and jurisdiction-specific policy before automatic purge.
Idempotency responses containing passenger names must be covered by that retention policy.

## Remaining production gates

Review account verification/reset/MFA, edge abuse controls, permission grants under the
actual managed PostgreSQL service, disaster recovery, PII retention, provider adapters,
ticket issuance, exporter wiring and representative load tests before public launch.
The code contains BOLA protections and strict DTOs, but broader OWASP API Top 10 coverage
also depends on deployment configuration, operational processes and independent testing.
