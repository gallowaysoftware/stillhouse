-- name: CreateAPIToken :one
INSERT INTO api_tokens (token_hash, tenant_id, user_id, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAPITokenByHash :one
-- Returns the token row + the owning user in one round trip. revoked_at
-- IS NULL is enforced inline so a revoked token is indistinguishable
-- from a missing one at the SQL layer.
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
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND t.revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens
SET last_used_at = NOW()
WHERE token_hash = $1;

-- name: RevokeAPIToken :exec
UPDATE api_tokens
SET revoked_at = NOW()
WHERE token_hash = $1
  AND revoked_at IS NULL;

-- name: ListAPITokensForUser :many
SELECT token_hash, tenant_id, user_id, name, last_used_at, revoked_at, created_at
FROM api_tokens
WHERE user_id = $1
ORDER BY created_at DESC;
