-- 000033_rls_assertions: make row-level security true of every
-- tenant-scoped table, rather than true of every table somebody
-- remembered.
--
-- Three gaps, each measured against pg_class on a live schema at
-- migration 32 rather than assumed:
--
--   * api_tokens carries tenant_id and had neither RLS enabled nor any
--     policy. Ownership was checked in Go only.
--   * recipe_version_sensory and recipe_version_whisky_sensory enable
--     RLS but never FORCE it. Latent — the app role is not their owner —
--     but 000004's own instruction is that a tenant-scoped table enables
--     *and* forces in the migration that creates it.
--   * Nothing asserted any of this. See the schema test added alongside
--     this migration, which enumerates every table carrying tenant_id
--     and fails if one of them is missing enable, force, or a policy.
--
-- users, tenants and sessions stay outside RLS on purpose: a login
-- lookup by email has to work before a tenant context exists, and
-- tenants is the authority on what a tenant id even is. That carve-out
-- is stated in 000001 and encoded as the allow-list in the schema test.

-- ------------------------------------------------------------------------
-- 1. The two sensory tables: enable was there, force was not.
-- ------------------------------------------------------------------------
ALTER TABLE recipe_version_sensory        FORCE ROW LEVEL SECURITY;
ALTER TABLE recipe_version_whisky_sensory FORCE ROW LEVEL SECURITY;

-- ------------------------------------------------------------------------
-- 2. api_tokens.
--
-- The reason this table was left out is real and has to be solved, not
-- ignored: the bearer-auth path looks a token up by hash *before* any
-- tenant context exists, because that lookup is what establishes the
-- tenant. Turning RLS on naively makes every API token stop working.
--
-- So the table goes under the same policy as everything else, and the
-- one query that legitimately runs before a tenant is known goes through
-- a keyhole: a NOLOGIN role holding BYPASSRLS owns two SECURITY DEFINER
-- functions — resolve a hash to its user, and stamp last_used_at — and
-- stillhouse_app is granted EXECUTE on those two and nothing else. The
-- app role's own reach into api_tokens is bounded by the policy, so the
-- management RPCs (issue / list / revoke) can no longer touch another
-- tenant's row even if the Go-side ownership check regresses.
-- ------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'stillhouse_auth') THEN
        CREATE ROLE stillhouse_auth NOLOGIN BYPASSRLS;
    END IF;
END $$;

ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_tokens FORCE  ROW LEVEL SECURITY;
CREATE POLICY api_tokens_tenant ON api_tokens FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- The keyhole role needs ordinary table privileges too; BYPASSRLS lifts
-- the policy, not the GRANT.
GRANT SELECT, UPDATE ON api_tokens TO stillhouse_auth;
GRANT SELECT           ON users      TO stillhouse_auth;

-- Resolve a token hash to its api_tokens row. RETURNS SETOF api_tokens
-- rather than a bespoke record shape so the row type stays the table's
-- own — the caller joins users itself, under no tenant context, which is
-- fine because users is deliberately outside RLS (000001). revoked_at IS
-- NULL is enforced here so a revoked token stays indistinguishable from
-- a missing one no matter who calls.
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

-- Stamp last_used_at. Separate function so the read path can stay STABLE
-- and so EXECUTE can be reasoned about one verb at a time.
CREATE OR REPLACE FUNCTION auth_touch_api_token(p_hash BYTEA)
RETURNS VOID
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    UPDATE api_tokens SET last_used_at = NOW() WHERE token_hash = p_hash;
$$;

ALTER FUNCTION auth_api_token(BYTEA)       OWNER TO stillhouse_auth;
ALTER FUNCTION auth_touch_api_token(BYTEA) OWNER TO stillhouse_auth;

REVOKE ALL ON FUNCTION auth_api_token(BYTEA)       FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_touch_api_token(BYTEA) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_api_token(BYTEA)       TO stillhouse_app;
GRANT EXECUTE ON FUNCTION auth_touch_api_token(BYTEA) TO stillhouse_app;
