-- Password reset tokens. Token VALUE never lives in the DB — we store the
-- SHA-256 hash and compare hashes at verify time, same pattern as session
-- tokens. That way a DB leak doesn't hand attackers a working reset URL.
CREATE TABLE password_reset_tokens (
    token_hash   BYTEA PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX password_reset_tokens_user_idx
    ON password_reset_tokens (user_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON password_reset_tokens TO stillhouse_app;
