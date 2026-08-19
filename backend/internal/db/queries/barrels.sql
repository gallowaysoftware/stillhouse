-- name: CreateBarrelAttributes :one
INSERT INTO barrel_attributes (
    container_id, tenant_id, cooperage_supplier, char_level, wood_species,
    prior_use, serial_burnin, rickhouse, row_position, level_position, column_position
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: GetBarrelAttributes :one
SELECT * FROM barrel_attributes WHERE container_id = $1;

-- name: ListBarrels :many
SELECT
    bc.id, bc.name, bc.kind, bc.capacity_l, bc.location, bc.notes, bc.archived,
    bc.current_volume_l, bc.current_abv_pct, bc.current_laa,
    bc.created_at, bc.updated_at,
    ba.cooperage_supplier, ba.char_level, ba.wood_species, ba.prior_use,
    ba.serial_burnin, ba.rickhouse, ba.row_position, ba.level_position, ba.column_position,
    ba.fill_date, ba.days_aged_at_dump
FROM bulk_containers bc
JOIN barrel_attributes ba ON ba.container_id = bc.id
WHERE bc.kind = 'barrel'
  AND (sqlc.arg('include_archived')::boolean OR NOT bc.archived)
ORDER BY ba.fill_date DESC NULLS LAST, bc.name;

-- name: SetBarrelFillDate :exec
UPDATE barrel_attributes SET fill_date = $2 WHERE container_id = $1;

-- name: SetBarrelDumpedClock :exec
UPDATE barrel_attributes
SET fill_date = NULL,
    days_aged_at_dump = $2
WHERE container_id = $1;

-- name: InsertBarrelEvent :one
-- volume_l / abv_pct / laa are the values AT 20 °C; observed_* preserve what
-- the operator read off the instrument. See migration 000023.
INSERT INTO barrel_events (
    tenant_id, container_id, kind, event_date,
    volume_l, abv_pct, laa, bulk_movement_id, location_after, notes, user_id,
    temperature_c, observed_volume_l, observed_density_kg_m3, volume_factor_c, strength_source
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
) RETURNING *;

-- name: ListBarrelEvents :many
SELECT * FROM barrel_events
WHERE container_id = $1
ORDER BY event_date DESC, created_at DESC;

-- name: GetBarrelEvent :one
SELECT * FROM barrel_events WHERE id = $1;

-- name: GetBulkMovementForBarrelEvent :one
SELECT * FROM bulk_movements WHERE id = $1;

-- name: VoidBarrelEvent :one
UPDATE barrel_events
SET voided_at = NOW(),
    voided_by = $2,
    voided_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;
