-- name: ListUsersForTenant :many
SELECT * FROM users
WHERE tenant_id = $1
ORDER BY role, display_name, email;
