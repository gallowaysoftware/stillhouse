-- name: ListAuditEvents :many
SELECT ae.*,
       u.email        AS user_email,
       u.display_name AS user_display_name
FROM audit_events ae
LEFT JOIN users u ON u.id = ae.user_id
WHERE (sqlc.narg('entity_type')::text IS NULL OR ae.entity_type = sqlc.narg('entity_type')::text)
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR ae.occurred_at >= sqlc.narg('from_ts')::timestamptz)
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR ae.occurred_at <  sqlc.narg('to_ts')::timestamptz)
ORDER BY ae.occurred_at DESC, ae.id DESC
LIMIT sqlc.arg('limit')::int OFFSET sqlc.arg('offset')::int;

-- name: CountAuditEvents :one
SELECT COUNT(*)::bigint AS count FROM audit_events
WHERE (sqlc.narg('entity_type')::text IS NULL OR entity_type = sqlc.narg('entity_type')::text)
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR occurred_at >= sqlc.narg('from_ts')::timestamptz)
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR occurred_at <  sqlc.narg('to_ts')::timestamptz);
