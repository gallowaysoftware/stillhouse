-- Personal access tokens — used by the MCP server (and any future
-- non-browser client) to authenticate as a specific user without going
-- through the cookie-session flow. The token VALUE never lives in the
-- DB; we store SHA-256(token) and compare hashes at verify time, same
-- pattern as password_reset_tokens.
--
-- tenant_id is denormalised onto the row so a single index lookup on
-- token_hash returns everything the request needs to bind the
-- per-request RLS GUC, without a second join through users.
CREATE TABLE api_tokens (
    token_hash    BYTEA PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    name          TEXT NOT NULL,
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX api_tokens_user_idx
    ON api_tokens (user_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON api_tokens TO stillhouse_app;
