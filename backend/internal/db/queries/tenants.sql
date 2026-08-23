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

-- name: ListAllTenants :many
-- Cross-tenant on purpose, and one of very few places that is. The alert
-- evaluator runs on a timer with no request behind it, so it has to find
-- the tenants itself before scoping to each in turn. tenants is outside
-- RLS (000001) precisely because it is the authority on what a tenant
-- id is.
SELECT * FROM tenants ORDER BY created_at;
