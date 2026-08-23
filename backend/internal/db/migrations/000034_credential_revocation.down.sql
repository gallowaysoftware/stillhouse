CREATE OR REPLACE FUNCTION auth_api_token(p_hash BYTEA)
RETURNS SETOF api_tokens
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT * FROM api_tokens
    WHERE token_hash = p_hash
      AND revoked_at IS NULL;
$$;
ALTER FUNCTION auth_api_token(BYTEA) OWNER TO stillhouse_auth;

ALTER TABLE api_tokens DROP COLUMN expires_at;
ALTER TABLE users      DROP COLUMN sessions_revoked_at;
