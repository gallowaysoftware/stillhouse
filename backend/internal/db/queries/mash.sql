-- name: NextMashNo :one
SELECT COALESCE(MAX(mash_no), 0)::int + 1 AS next FROM mash_runs;

-- name: CreateMashRun :one
INSERT INTO mash_runs (
    tenant_id, recipe_version_id, mash_no, mash_date, status, notes
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetMashRun :one
SELECT * FROM mash_runs WHERE id = $1;

-- name: ListMashRuns :many
SELECT mr.*,
       r.name        AS recipe_name,
       rv.version_no AS recipe_version_no
FROM mash_runs mr
JOIN recipe_versions rv ON rv.id = mr.recipe_version_id
JOIN recipes r          ON r.id = rv.recipe_id
WHERE (sqlc.narg('recipe_id')::uuid IS NULL OR r.id = sqlc.narg('recipe_id')::uuid)
  AND (sqlc.narg('status')::mash_status IS NULL OR mr.status = sqlc.narg('status')::mash_status)
ORDER BY mr.mash_date DESC, mr.mash_no DESC;

-- name: UpdateMashStatus :one
UPDATE mash_runs SET status = $2 WHERE id = $1 RETURNING *;

-- name: AddMashIngredient :one
INSERT INTO mash_ingredient_usage (
    tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListMashIngredients :many
SELECT miu.*,
       m.name AS material_name,
       m.kind AS material_kind,
       m.extract_pct AS material_extract_pct,
       ml.supplier_lot AS supplier_lot,
       ml.received_at  AS lot_received_at
FROM mash_ingredient_usage miu
JOIN materials m            ON m.id = miu.material_id
LEFT JOIN material_lots ml  ON ml.id = miu.material_lot_id
WHERE miu.mash_run_id = $1
ORDER BY m.name;

-- name: DebitMaterialLot :one
-- Debit the on-hand quantity of a lot when a mash consumes from it. Returns
-- the updated row so the caller can warn if the lot is now exhausted.
UPDATE material_lots
SET quantity_on_hand = quantity_on_hand - $2
WHERE id = $1 AND quantity_on_hand >= $2
RETURNING *;

-- name: AddMashMetric :one
INSERT INTO mash_metrics (
    tenant_id, mash_run_id, kind, value, unit, observed_at, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListMashMetrics :many
SELECT * FROM mash_metrics
WHERE mash_run_id = $1
ORDER BY observed_at;
