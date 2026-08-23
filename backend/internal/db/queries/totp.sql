-- name: GetUserTOTP :one
SELECT * FROM user_totp WHERE user_id = $1;

-- name: UpsertUserTOTP :one
-- Starting enrolment replaces any unfinished attempt. It must NOT
-- replace a confirmed one: overwriting a working second factor from an
-- unauthenticated position is a lockout, and from an authenticated one
-- it should be a deliberate disable-then-enrol.
INSERT INTO user_totp (user_id, secret_sealed)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET secret_sealed = EXCLUDED.secret_sealed,
    confirmed_at  = NULL,
    last_used_step = NULL
WHERE user_totp.confirmed_at IS NULL
RETURNING *;

-- name: ConfirmUserTOTP :one
UPDATE user_totp
SET confirmed_at = NOW(), last_used_step = $2
WHERE user_id = $1 AND confirmed_at IS NULL
RETURNING *;

-- name: RecordTOTPStep :exec
-- The replay guard. Refusing anything at or below the last accepted step
-- is what stops a code being used twice inside its window.
UPDATE user_totp SET last_used_step = $2 WHERE user_id = $1;

-- name: DeleteUserTOTP :exec
DELETE FROM user_totp WHERE user_id = $1;

-- name: DeleteTOTPRecoveryCodes :exec
DELETE FROM user_totp_recovery_codes WHERE user_id = $1;

-- name: CreateTOTPRecoveryCode :exec
INSERT INTO user_totp_recovery_codes (code_hash, user_id) VALUES ($1, $2);

-- name: ConsumeTOTPRecoveryCode :one
-- Single use, enforced in the UPDATE rather than in Go: two tabs
-- submitting the same code must not both succeed.
UPDATE user_totp_recovery_codes
SET used_at = NOW()
WHERE code_hash = $1 AND user_id = $2 AND used_at IS NULL
RETURNING *;

-- name: CountUnusedTOTPRecoveryCodes :one
SELECT COUNT(*)::INTEGER FROM user_totp_recovery_codes
WHERE user_id = $1 AND used_at IS NULL;
