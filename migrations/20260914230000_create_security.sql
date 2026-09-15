-- +goose Up
CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (expires_at > created_at)
);
CREATE INDEX auth_sessions_user_idx ON auth_sessions(user_id);

CREATE TABLE auth_credentials (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash)=32),
    session_id UUID NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('ACCESS','REFRESH')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX auth_credentials_session_idx ON auth_credentials(session_id);
CREATE INDEX auth_credentials_expiration_idx ON auth_credentials(expires_at);

CREATE TABLE security_audit_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('REGISTER','LOGIN','LOGOUT','LOGOUT_ALL','REFRESH_REUSE')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX security_audit_records_created_idx ON security_audit_records(created_at);

CREATE TABLE rate_limit_buckets (
    key_hash BYTEA NOT NULL CHECK (octet_length(key_hash)=32),
    window_start TIMESTAMPTZ NOT NULL,
    requests INTEGER NOT NULL CHECK (requests>0),
    PRIMARY KEY(key_hash,window_start)
);

-- +goose Down
DROP TABLE rate_limit_buckets;
DROP TABLE security_audit_records;
DROP TABLE auth_credentials;
DROP TABLE auth_sessions;
