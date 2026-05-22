-- name: InsertAuditEvent :one
INSERT INTO audit_events (
    tenant_id, user_id, entity_type, entity_id, action, payload
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;
