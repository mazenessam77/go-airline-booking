# Architecture and decisions

The API and worker share domain services and PostgreSQL. Domain packages never depend
on HTTP. HTTP DTOs explicitly select fields; travel-document ciphertext is not exposed.

## ADR 001: Authentication

Use opaque 256-bit bearer access tokens (10 minutes) and rotating refresh tokens
within an absolute 30-day session. PostgreSQL stores SHA-256 token hashes. A consumed
refresh token revokes the entire session when reused. Clients must serialize refresh
calls; retrying a refresh after a lost response requires logging in again. Logout-all
serializes against login and revokes all existing sessions. Roles are assigned by
trusted operators, never registration JSON. Booking ownership applies to every role.

Argon2id uses 19 MiB, two iterations, one lane, a random 16-byte salt and a 32-byte
output. Each process admits at most four simultaneous hashing operations. Measure
latency and memory on deployment hardware before increasing the cost. Registration
returns the same accepted response for existing and new addresses. Login runs a
dummy hash for a missing account. No claim of perfectly identical network timing is made.

The HTTP API does not set authentication cookies. Native applications must store
refresh tokens in the OS credential store. A browser application should use a
backend-for-frontend that stores API credentials server-side; its cookies and CSRF
controls are the BFF's responsibility. Direct browser clients may keep access tokens
in memory, but must not persist refresh tokens in localStorage. Browser persistence,
email verification, password reset and MFA need additional product and delivery design.

Reference: [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## ADR 002: Seat locking

Use READ COMMITTED transactions with explicit row locks. Acquire the booking and
segment, flight eligibility, passengers, seats ordered by UUID, then active assignments.
Expiration workers acquire bookings with SKIP LOCKED and lock the entire batch's
inventory in UUID order. Reclamation never locks or updates another booking.
The old booking is expired later by the worker. Every lifecycle writer must preserve
this ordering. No network calls occur while these transactions are open.

Read clock_timestamp after waiting for inventory locks; CURRENT_TIMESTAMP is the
transaction start time. Retrying a hold never extends its original expiration.
Partial unique indexes prevent duplicate active seat and passenger assignments;
composite foreign keys enforce booking, passenger, segment and flight relationships.
Application checks reject contradictory inventory state rather than repairing it blindly.

Reference: [PostgreSQL 17 locking](https://www.postgresql.org/docs/17/explicit-locking.html).

## ADR 003: Pricing and payments

Quotes snapshot prices, taxes and refund eligibility from locked fare offers, in integer minor units,
with a ten-minute expiration. Taxes in fare_offers are per passenger. A booking
consumes one authenticated user's quote under a row lock and unique constraint.
All segments currently require the same passenger count and currency. Seat selection
must match the purchased fare cabin. Hold expiry never exceeds departure.
Existing quotes/bookings without historical refund-policy evidence default to no automatic
refund and require operator review. Later fare-policy edits do not change new bookings' snapshots.

Payment requests snapshot the booking amount. Provider keys are stable across retries.
Provider calls happen after the preparation transaction commits. Unknown outcomes
require provider lookup or retry with the same provider idempotency key. Provider
adapters must guarantee idempotency for the full reconciliation retention period.
Signed callbacks are deduplicated by provider event ID; terminal success is not
reversed by a later failure. A success after hold expiry creates a pending refund.
Refund calls also use stable keys. A successful refund never recreates inventory.

The fake provider is for tests only. Production API and worker intentionally have no
provider adapter or webhook secret configured, so payment/refund HTTP requests return
503 after ownership checks. Real-provider authorization, credentials, signature format,
reconciliation semantics and PCI scope must be reviewed before enabling dispatch.
TicketIssuer is an integration interface, not a ticket-issuance implementation.

## ADR 004: Idempotency and outbox

Retry-sensitive mutations require a 16–128 character Idempotency-Key. Records bind
user, route, key and normalized payload hash. An advisory transaction lock serializes
identical keys; the domain write and saved response commit together. A different
payload returns 409. Configure a minimum 24-hour replay retention window; do not
delete payment provider keys or webhook IDs with ordinary HTTP replay records.

Outbox events commit with state changes. Workers claim events with SKIP LOCKED,
30-second leases and unique lease tokens, publish outside transactions, and acknowledge
only their lease. Retries use exponential backoff and dead-letter after ten attempts.
Delivery is at least once. Consumers commit an event receipt with their side effect.
The default domain-ledger consumer records durable receipts; it does not send email,
issue tickets, or claim to notify customers. Payment events require the provider adapter.

## Operational limits

Search pages contain at most 100 flights or seats. Dates use UTC calendar boundaries.
Use the last departure and ID as the next flight cursor; seat cursors use the last ID.
Bookings allow at most four segments and nine passengers. Guest booking and travel
document ingestion are not exposed. Booking cancellation through `/cancel` applies
to unpaid DRAFT/HELD bookings; paid cancellation uses the refundable-fare refund flow.
