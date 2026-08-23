-- name: CreateUser :one
INSERT INTO users (
    tenant_id, email, password_hash, display_name, role
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: ListUsersByEmail :many
-- An email address no longer identifies one account: it is unique per
-- tenant, so the outside bookkeeper can hold one at each distillery they
-- work for. Every caller that starts from an address alone — login,
-- password reset — has to reckon with a set.
--
-- Ordered by created_at so the answer is stable, and capped: the cost of
-- a login attempt is one password verification per row returned, and
-- that must not be something an attacker can inflate by registering
-- accounts. Nobody legitimately holds accounts at more than a handful of
-- distilleries under one address.
SELECT * FROM users WHERE email = $1 ORDER BY created_at LIMIT 8;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;
