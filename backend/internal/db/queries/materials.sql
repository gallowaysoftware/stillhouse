-- name: CreateMaterial :one
INSERT INTO materials (
    tenant_id, name, kind, uom, supplier, notes, extract_fraction, moisture_fraction, cereal
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: UpdateMaterial :one
UPDATE materials
SET name         = $2,
    uom          = $3,
    supplier     = $4,
    notes        = $5,
    extract_fraction  = $6,
    moisture_fraction = $7,
    cereal       = $8
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

-- name: SetMaterialReorder :one
UPDATE materials
SET reorder_point         = sqlc.narg(reorder_point)::double precision,
    reorder_quantity      = sqlc.narg(reorder_quantity)::double precision,
    lead_time_days        = sqlc.narg(lead_time_days)::int,
    preferred_supplier_id = sqlc.narg(preferred_supplier_id)::uuid
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: MaterialCover :many
-- On hand, what it is being used at, and how long that lasts.
--
-- The consumption rate is what actually went into mashes over the window,
-- divided by the window. Materials that nothing has consumed come back
-- with a rate of zero, and cover is then unknown rather than infinite —
-- a material nobody has used yet may be about to be used daily.
--
-- Deliberately generalises what the stamp panel already does for excise
-- stamps: bottles a day over the last thirty, divided into what is left.
SELECT m.id, m.name, m.kind, m.uom, m.archived,
       m.reorder_point, m.reorder_quantity, m.lead_time_days,
       COALESCE(s.name, '') AS preferred_supplier_name,
       COALESCE(oh.qty, 0)::double precision AS on_hand,
       COALESCE(used.qty, 0)::double precision AS used_in_window,
       COALESCE(oo.qty, 0)::double precision AS on_order
FROM materials m
LEFT JOIN suppliers s ON s.id = m.preferred_supplier_id
LEFT JOIN LATERAL (
    SELECT SUM(ml.quantity_on_hand) AS qty
    FROM material_lots ml WHERE ml.material_id = m.id
) oh ON TRUE
LEFT JOIN LATERAL (
    SELECT SUM(u.quantity_used) AS qty
    FROM mash_ingredient_usage u
    JOIN mash_runs r ON r.id = u.mash_run_id
    WHERE u.material_id = m.id
      AND r.created_at >= NOW() - (sqlc.arg(window_days)::int || ' days')::interval
) used ON TRUE
LEFT JOIN LATERAL (
    -- Already ordered and not yet received, so a reorder alert does not
    -- fire on something that is already on a truck.
    SELECT SUM(l.quantity_ordered - l.quantity_received) AS qty
    FROM purchase_order_lines l
    JOIN purchase_orders po ON po.id = l.purchase_order_id
    WHERE l.material_id = m.id
      AND po.status IN ('placed', 'partially_received')
      AND l.quantity_received < l.quantity_ordered
) oo ON TRUE
WHERE NOT m.archived
ORDER BY m.kind, m.name;

-- name: MaterialsBelowReorderPoint :many
-- For the alert evaluator. Only materials with a reorder point recorded:
-- one Stillhouse guessed would fire at a level nobody chose, and an alert
-- people did not choose is an alert they learn to dismiss.
SELECT m.id, m.name, m.uom, m.reorder_point, m.lead_time_days,
       COALESCE(oh.qty, 0)::double precision AS on_hand,
       COALESCE(oo.qty, 0)::double precision AS on_order
FROM materials m
LEFT JOIN LATERAL (
    SELECT SUM(ml.quantity_on_hand) AS qty
    FROM material_lots ml WHERE ml.material_id = m.id
) oh ON TRUE
LEFT JOIN LATERAL (
    SELECT SUM(l.quantity_ordered - l.quantity_received) AS qty
    FROM purchase_order_lines l
    JOIN purchase_orders po ON po.id = l.purchase_order_id
    WHERE l.material_id = m.id
      AND po.status IN ('placed', 'partially_received')
      AND l.quantity_received < l.quantity_ordered
) oo ON TRUE
WHERE NOT m.archived
  AND m.reorder_point IS NOT NULL
  AND COALESCE(oh.qty, 0) + COALESCE(oo.qty, 0) <= m.reorder_point
ORDER BY m.name;
