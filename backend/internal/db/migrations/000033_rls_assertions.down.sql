DROP FUNCTION IF EXISTS auth_touch_api_token(BYTEA);
DROP FUNCTION IF EXISTS auth_api_token(BYTEA);

DROP POLICY IF EXISTS api_tokens_tenant ON api_tokens;
ALTER TABLE api_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE api_tokens DISABLE ROW LEVEL SECURITY;

REVOKE ALL ON api_tokens FROM stillhouse_auth;
REVOKE ALL ON users      FROM stillhouse_auth;
-- The role itself is left in place: DROP ROLE fails if anything else in
-- the cluster still references it, and an unprivileged NOLOGIN role with
-- no grants is inert.

ALTER TABLE recipe_version_whisky_sensory NO FORCE ROW LEVEL SECURITY;
ALTER TABLE recipe_version_sensory        NO FORCE ROW LEVEL SECURITY;
