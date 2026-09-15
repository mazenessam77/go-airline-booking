-- Run with a dedicated maintenance role; review retention periods before production.
-- Each statement is bounded and may be scheduled repeatedly until no rows remain.
DELETE FROM rate_limit_buckets WHERE ctid IN (
    SELECT ctid FROM rate_limit_buckets WHERE window_start<clock_timestamp()-interval '1 day' LIMIT 1000
);
DELETE FROM idempotency_records WHERE ctid IN (
    SELECT ctid FROM idempotency_records WHERE created_at<clock_timestamp()-interval '1 day' LIMIT 1000
);
DELETE FROM auth_sessions WHERE id IN (
    SELECT id FROM auth_sessions WHERE expires_at<clock_timestamp()-interval '1 day' LIMIT 1000
);
DELETE FROM security_audit_records WHERE id IN (
    SELECT id FROM security_audit_records WHERE created_at<clock_timestamp()-interval '90 days' LIMIT 1000
);
-- Passenger retention requires an approved jurisdiction-specific policy and legal-hold checks.
-- Do not automatically purge booking, payment, refund or webhook evidence.
