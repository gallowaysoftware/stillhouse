-- name: UpdateUserPassword :one
UPDATE users SET password_hash = $2 WHERE id = $1 RETURNING *;

-- name: MarkUserEmailVerified :one
UPDATE users
SET email_verified_at = NOW()
WHERE id = $1 AND email_verified_at IS NULL
RETURNING *;
