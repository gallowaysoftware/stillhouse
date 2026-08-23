-- 000034_credential_revocation: make changing a password actually take
-- something away.
--
-- Before this, ResetPassword and ChangeMyPassword updated the hash and
-- returned. The comment in user.go said so outright — "the session stays
-- valid (we don't force re-login)". Sessions last seven days with no idle
-- timeout and API tokens had no expiry column at all, so an attacker who
-- phished a password and minted a token kept both after the victim did
-- the one thing everybody knows to do.
--
-- Two columns close it.

-- ------------------------------------------------------------------------
-- users.sessions_revoked_at — the watermark every session is measured
-- against.
--
-- Sessions live in scs's opaque blob, so there is no session table to
-- delete rows from by user. Instead each session records when it
-- authenticated, and a session whose authentication predates this
-- watermark is dead. One UPDATE invalidates every session a user has,
-- everywhere, including ones on devices they no longer possess — which
-- is the whole point of changing a password you think has leaked.
--
-- NULL means nothing has ever been revoked, so pre-existing sessions
-- keep working until the first credential change. A session created
-- before this migration carries no authenticated-at stamp and is treated
-- as older than any watermark, which is the safe direction.
-- ------------------------------------------------------------------------
ALTER TABLE users
    ADD COLUMN sessions_revoked_at TIMESTAMPTZ;

-- ------------------------------------------------------------------------
-- api_tokens.expires_at — a token that outlives its usefulness.
--
-- NULL means no expiry. Every token issued before this migration is
-- NULL, because that is what it already was in fact; the alternative,
-- backdating an expiry onto tokens people are holding, breaks a working
-- MCP client on a phone in a rackhouse with no explanation. New tokens
-- get a default lifetime at issue time (see IssueAPIToken).
-- ------------------------------------------------------------------------
ALTER TABLE api_tokens
    ADD COLUMN expires_at TIMESTAMPTZ;

-- The keyhole from 000033 is the only place a token is resolved for
-- authentication, so it is the only place expiry has to be enforced —
-- and enforcing it here means no caller can forget to.
CREATE OR REPLACE FUNCTION auth_api_token(p_hash BYTEA)
RETURNS SETOF api_tokens
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT * FROM api_tokens
    WHERE token_hash = p_hash
      AND revoked_at IS NULL
      AND (expires_at IS NULL OR expires_at > NOW());
$$;
ALTER FUNCTION auth_api_token(BYTEA) OWNER TO stillhouse_auth;
