-- name: CreateTenant :one
INSERT INTO tenants (
    name, cra_spirits_licence_number, excise_warehouse_licence_number, default_jurisdiction
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: GetTenantByID :one
SELECT * FROM tenants WHERE id = $1;

-- name: CountTenants :one
SELECT COUNT(*)::bigint AS count FROM tenants;
