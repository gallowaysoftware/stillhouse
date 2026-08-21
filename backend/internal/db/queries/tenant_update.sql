-- name: SetDutyPointEffectiveFrom :one
-- Moves the cutover. Not exposed in the UI: the date is set once, when the
-- tenant is created or when this migration ran, and moving it re-attributes
-- duty across events that may already have been filed. Here so a support
-- path exists that is deliberate rather than improvised.
UPDATE tenants SET duty_point_effective_from = $2 WHERE id = $1 RETURNING *;

-- name: UpdateTenant :one
UPDATE tenants
SET name                            = $2,
    cra_spirits_licence_number      = $3,
    excise_warehouse_licence_number = $4,
    default_jurisdiction            = $5
WHERE id = $1
RETURNING *;
