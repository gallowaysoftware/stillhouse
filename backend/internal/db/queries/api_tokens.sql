-- name: CreateAPIToken :one
-- expires_at NULL means the token never expires. That is a deliberate
-- choice at the RPC layer, not a default — see IssueAPIToken.
INSERT INTO api_tokens (token_hash, tenant_id, user_id, name, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAPITokenByHash :one
-- The pre-tenant keyhole. Bearer auth has to resolve a token hash to its
-- owner before any tenant context exists — that lookup is what
-- establishes the tenant — so it cannot satisfy the RLS policy migration
-- 000033 put on api_tokens. It goes through a SECURITY DEFINER function
-- owned by stillhouse_auth (NOLOGIN, BYPASSRLS) instead, which is the
-- only reach the app role has into this table without a tenant.
-- revoked_at IS NULL is enforced inside the function, so a revoked token
-- stays indistinguishable from a missing one.
SELECT
    t.token_hash,
    t.tenant_id,
    t.user_id,
    t.name,
    t.last_used_at,
    t.revoked_at,
    t.created_at,
    u.email        AS user_email,
    u.display_name AS user_display_name,
    u.role         AS user_role
FROM auth_api_token(@token_hash::BYTEA) t
JOIN users u ON u.id = t.user_id;

-- name: TouchAPIToken :exec
-- Same keyhole, write half. Best-effort last_used_at stamp on the auth
-- path, which likewise has no tenant context to offer.
SELECT auth_touch_api_token(@token_hash::BYTEA);

-- name: GetAPITokenRowByHash :one
-- Like GetAPITokenByHash but doesn't filter on revoked_at; used by the
-- token-management RPC to ownership-check before revoke.
SELECT * FROM api_tokens WHERE token_hash = $1;

-- name: RevokeAPIToken :one
UPDATE api_tokens
SET revoked_at = NOW()
WHERE token_hash = $1
  AND revoked_at IS NULL
RETURNING *;

-- name: ListAPITokensForUser :many
SELECT * FROM api_tokens
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokeAllAPITokensForUser :many
-- The "revoke everything" half of a credential reset. Returns the rows it
-- revoked so the caller can report a count and audit it; already-revoked
-- tokens are left alone so the count means what it says.
UPDATE api_tokens
SET revoked_at = NOW()
WHERE user_id = $1
  AND revoked_at IS NULL
RETURNING *;
