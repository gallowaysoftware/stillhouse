-- name: CreatePasswordResetToken :one
INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ConsumePasswordResetToken :one
-- Single-use semantics: WHERE used_at IS NULL guarantees the same token
-- can't be redeemed twice. Expiry check inline so we don't accidentally
-- accept stale tokens.
UPDATE password_reset_tokens
SET used_at = NOW()
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > NOW()
RETURNING *;
