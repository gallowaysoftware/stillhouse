-- name: ClaimIdempotencyKey :one
-- Returns a row only when this request is the first with the key.
INSERT INTO idempotent_requests (tenant_id, user_id, key, procedure)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
RETURNING created_at;

-- name: GetIdempotentRequest :one
SELECT procedure, response, created_at FROM idempotent_requests
WHERE tenant_id = $1 AND user_id = $2 AND key = $3;

-- name: StoreIdempotentResponse :exec
UPDATE idempotent_requests SET response = $4
WHERE tenant_id = $1 AND user_id = $2 AND key = $3;

-- name: ReleaseIdempotencyKey :exec
DELETE FROM idempotent_requests
WHERE tenant_id = $1 AND user_id = $2 AND key = $3 AND response IS NULL;

-- name: ForgetOldIdempotentRequests :exec
DELETE FROM idempotent_requests WHERE created_at < NOW() - INTERVAL '30 days';
