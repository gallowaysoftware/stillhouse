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

-- name: DeleteTenant :exec
-- Hard delete — every FK to tenants is ON DELETE CASCADE so this wipes
-- the entire tenant footprint (users, recipes, mashes, ferments,
-- distillations, barrels, bulk, bottling, removals, B266 history,
-- audit_events, etc) in one go.
DELETE FROM tenants WHERE id = $1;
