-- name: CreateInviteCode :one
INSERT INTO invite_codes (
    code, created_by_user_id, created_by_tenant_id, note, expires_at
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetInviteCode :one
-- Public lookup at signup time. Caller must check redeemed_at / revoked_at /
-- expires_at to decide if the code is actually usable.
SELECT * FROM invite_codes WHERE code = $1;

-- name: ListInviteCodesByCreator :many
SELECT * FROM invite_codes
WHERE created_by_user_id = $1
ORDER BY created_at DESC;

-- name: RedeemInviteCode :one
UPDATE invite_codes
SET redeemed_at        = NOW(),
    redeemed_email     = $2,
    redeemed_tenant_id = $3
WHERE code = $1
  AND redeemed_at IS NULL
  AND revoked_at  IS NULL
  AND (expires_at IS NULL OR expires_at > NOW())
RETURNING *;

-- name: RevokeInviteCode :one
UPDATE invite_codes
SET revoked_at = NOW()
WHERE code = $1
  AND redeemed_at IS NULL
  AND revoked_at  IS NULL
RETURNING *;
