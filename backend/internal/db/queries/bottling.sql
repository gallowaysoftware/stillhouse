-- name: NextBottlingRunNo :one
SELECT COALESCE(MAX(run_no), 0)::int + 1 AS next FROM bottling_runs;

-- name: CreateBottlingRun :one
INSERT INTO bottling_runs (
    tenant_id, run_no, product_id, source_container_id, destination_jurisdiction,
    bottling_date, bottle_count, bottling_loss_l, lot_code,
    tank_gauge_volume_l, tank_gauge_abv_pct, tank_gauge_laa,
    bulk_movement_id, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
) RETURNING *;

-- name: GetBottlingRun :one
SELECT * FROM bottling_runs WHERE id = $1;

-- name: ListBottlingRuns :many
SELECT br.*,
       p.name AS product_name,
       p.bottle_size_ml AS product_bottle_size_ml,
       p.target_abv_pct AS product_target_abv_pct
FROM bottling_runs br
JOIN products p ON p.id = br.product_id
WHERE (sqlc.narg('period_start')::date IS NULL OR br.bottling_date >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR br.bottling_date <= sqlc.narg('period_end')::date)
ORDER BY br.bottling_date DESC, br.run_no DESC
LIMIT $1 OFFSET $2;

-- name: CountBottlingRuns :one
SELECT COUNT(*)::int AS total
FROM bottling_runs
WHERE (sqlc.narg('period_start')::date IS NULL OR bottling_date >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR bottling_date <= sqlc.narg('period_end')::date);

-- name: CreateBottlingRunStampUsage :one
INSERT INTO bottling_run_stamp_usage (
    tenant_id, bottling_run_id, stamp_order_id, bottle_count, serial_start, serial_end, voids
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListBottlingRunStampUsage :many
SELECT brs.*,
       eso.jurisdiction AS jurisdiction,
       eso.serial_start AS order_serial_start,
       eso.serial_end   AS order_serial_end
FROM bottling_run_stamp_usage brs
JOIN excise_stamp_orders eso ON eso.id = brs.stamp_order_id
WHERE brs.bottling_run_id = $1
ORDER BY brs.created_at;

-- name: UpsertPackagedInventory :one
INSERT INTO packaged_inventory (
    tenant_id, product_id, lot_code, jurisdiction, bottling_run_id,
    bottles_on_hand, bottles_packaged
) VALUES (
    $1, $2, $3, $4, $5, $6, $6
)
ON CONFLICT (product_id, lot_code, jurisdiction) DO UPDATE
SET bottles_on_hand  = packaged_inventory.bottles_on_hand  + EXCLUDED.bottles_on_hand,
    bottles_packaged = packaged_inventory.bottles_packaged + EXCLUDED.bottles_packaged
RETURNING *;

-- name: ListPackagedInventory :many
SELECT pi.*,
       p.name           AS product_name,
       p.bottle_size_ml AS bottle_size_ml,
       p.target_abv_pct AS target_abv_pct
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id
WHERE pi.bottles_on_hand > 0
   OR sqlc.arg('include_empty')::boolean
ORDER BY p.name, pi.jurisdiction, pi.lot_code;

-- name: VoidBottlingRun :one
UPDATE bottling_runs
SET voided_at = NOW(),
    voided_by = $2,
    voided_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;

-- name: DecrementPackagedInventoryByRun :one
-- Reverse the upsert that bottling did. We don't delete the row even if it
-- zeroes out — keeping the row preserves the (product, lot_code, jurisdiction)
-- key for audit traceability.
UPDATE packaged_inventory
SET bottles_on_hand  = bottles_on_hand  - $2,
    bottles_packaged = bottles_packaged - $2
WHERE id = $1 AND bottles_on_hand >= $2
RETURNING *;

-- name: PackagedInventoryByLot :one
SELECT * FROM packaged_inventory
WHERE product_id = $1 AND lot_code = $2 AND jurisdiction = $3;

-- name: ListBottlingRunsForProduct :many
-- Active (non-voided) bottling runs for a product, oldest first so the
-- cost rollup walks them in chronological order.
SELECT id, run_no, source_container_id, bottling_date, bottle_count
FROM bottling_runs
WHERE product_id = $1 AND voided_at IS NULL
ORDER BY bottling_date, run_no;

-- name: DecrementStampOrderApplied :one
UPDATE excise_stamp_orders
SET quantity_applied = quantity_applied - $2
WHERE id = $1 AND quantity_applied >= $2
RETURNING *;
