-- name: UpdateUserPassword :one
-- Writing a new password revokes every session that authenticated before
-- this moment. It happens in the same statement as the hash update so no
-- caller can do the half that feels like the fix and skip the half that
-- is. The caller that still holds a live session (ChangeMyPassword)
-- re-stamps its own session from the returned sessions_revoked_at.
UPDATE users
SET password_hash       = $2,
    sessions_revoked_at = NOW()
WHERE id = $1
RETURNING *;

-- name: MarkUserEmailVerified :one
UPDATE users
SET email_verified_at = NOW()
WHERE id = $1 AND email_verified_at IS NULL
RETURNING *;
