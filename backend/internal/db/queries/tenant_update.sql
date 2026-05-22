-- name: UpdateTenant :one
UPDATE tenants
SET name                            = $2,
    cra_spirits_licence_number      = $3,
    excise_warehouse_licence_number = $4,
    default_jurisdiction            = $5
WHERE id = $1
RETURNING *;
