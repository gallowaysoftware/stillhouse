-- name: CreateMaterial :one
INSERT INTO materials (
    tenant_id, name, kind, uom, supplier, notes, extract_pct, moisture_pct
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: UpdateMaterial :one
UPDATE materials
SET name         = $2,
    uom          = $3,
    supplier     = $4,
    notes        = $5,
    extract_pct  = $6,
    moisture_pct = $7
WHERE id = $1
RETURNING *;

-- name: GetMaterial :one
SELECT * FROM materials WHERE id = $1;

-- name: ListMaterials :many
SELECT * FROM materials
WHERE (sqlc.narg('kind')::material_kind IS NULL OR kind = sqlc.narg('kind')::material_kind)
  AND (sqlc.arg('include_archived')::boolean OR NOT archived)
ORDER BY kind, name;

-- name: ArchiveMaterial :one
UPDATE materials SET archived = TRUE WHERE id = $1 RETURNING *;

-- name: UnarchiveMaterial :one
UPDATE materials SET archived = FALSE WHERE id = $1 RETURNING *;

-- name: CreateMaterialLot :one
INSERT INTO material_lots (
    tenant_id, material_id, supplier_lot, quantity_received, quantity_on_hand, received_at, notes, unit_cost_cad
) VALUES (
    $1, $2, $3, $4, $4, $5, $6, $7
) RETURNING *;

-- name: GetMaterialLot :one
SELECT * FROM material_lots WHERE id = $1;

-- name: ListMaterialLots :many
SELECT * FROM material_lots
WHERE (sqlc.narg('material_id')::uuid IS NULL OR material_id = sqlc.narg('material_id')::uuid)
  AND (NOT sqlc.arg('on_hand_only')::boolean OR quantity_on_hand > 0)
ORDER BY received_at DESC;
