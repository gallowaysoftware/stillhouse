-- name: NextStockCountNo :one
SELECT COALESCE(MAX(count_no), 0)::INTEGER + 1 AS next FROM stock_counts;

-- name: CreateStockCount :one
INSERT INTO stock_counts (
    tenant_id, count_no, name, scope, location_id, notes, created_by
) VALUES ($1, $2, $3, sqlc.arg(scope)::stock_count_scope, $4, $5, $6)
RETURNING *;

-- name: GetStockCount :one
SELECT * FROM stock_counts WHERE id = $1;

-- name: GetStockCountForUpdate :one
SELECT * FROM stock_counts WHERE id = $1 FOR UPDATE;

-- name: ListStockCounts :many
SELECT c.*, COALESCE(l.name, '') AS location_name,
       COUNT(ln.id)::int AS line_count,
       COUNT(ln.id) FILTER (WHERE ln.counted_quantity IS NOT NULL)::int AS counted_lines,
       COUNT(ln.id) FILTER (WHERE ln.posted_at IS NOT NULL)::int AS posted_lines
FROM stock_counts c
LEFT JOIN locations l ON l.id = c.location_id
LEFT JOIN stock_count_lines ln ON ln.stock_count_id = c.id
GROUP BY c.id, l.name
ORDER BY c.opened_at DESC;

-- name: SetStockCountStatus :one
UPDATE stock_counts
SET status        = sqlc.arg(status)::stock_count_status,
    counted_at    = CASE WHEN sqlc.arg(status)::stock_count_status = 'counted'
                          AND counted_at IS NULL THEN NOW() ELSE counted_at END,
    posted_at     = CASE WHEN sqlc.arg(status)::stock_count_status = 'posted'
                         THEN NOW() ELSE posted_at END,
    posted_by     = CASE WHEN sqlc.arg(status)::stock_count_status = 'posted'
                         THEN sqlc.narg(actor)::uuid ELSE posted_by END,
    cancel_reason = CASE WHEN sqlc.arg(status)::stock_count_status = 'cancelled'
                         THEN sqlc.arg(cancel_reason)::text ELSE cancel_reason END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AddStockCountLine :one
INSERT INTO stock_count_lines (
    tenant_id, stock_count_id, bulk_container_id, packaged_inventory_id,
    material_lot_id, book_quantity, uom
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: SetStockCountLineCount :one
UPDATE stock_count_lines
SET counted_quantity = sqlc.arg(counted_quantity)::double precision,
    counted_abv_pct  = sqlc.narg(counted_abv_pct)::double precision,
    reason           = sqlc.narg(reason)::inventory_adjustment_reason,
    explanation      = sqlc.arg(explanation)::text,
    counted_by       = sqlc.arg(counted_by)::text,
    notes            = sqlc.arg(notes)::text
WHERE id = sqlc.arg(id) AND posted_at IS NULL
RETURNING *;

-- name: ListStockCountLines :many
SELECT ln.*,
       COALESCE(bc.name, '')      AS container_name,
       COALESCE(pi.lot_code, '')  AS lot_code,
       COALESCE(pp.name, '')      AS lot_product_name,
       COALESCE(m.name, '')       AS material_name,
       COALESCE(ml.supplier_lot, '') AS supplier_lot
FROM stock_count_lines ln
LEFT JOIN bulk_containers bc     ON bc.id = ln.bulk_container_id
LEFT JOIN packaged_inventory pi  ON pi.id = ln.packaged_inventory_id
LEFT JOIN products pp            ON pp.id = pi.product_id
LEFT JOIN material_lots ml       ON ml.id = ln.material_lot_id
LEFT JOIN materials m            ON m.id = ml.material_id
WHERE ln.stock_count_id = $1
ORDER BY bc.name NULLS LAST, pp.name NULLS LAST, m.name NULLS LAST;

-- name: MarkStockCountLinePosted :exec
UPDATE stock_count_lines
SET posted_at = NOW(), adjustment_id = sqlc.narg(adjustment_id)::uuid
WHERE id = sqlc.arg(id);

-- name: RecordPackagedAdjustment :one
INSERT INTO packaged_adjustments (
    tenant_id, packaged_inventory_id, occurred_on, bottles_delta,
    book_bottles, counted_bottles, laa_delta, reason, explanation,
    stock_count_id, recorded_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    sqlc.arg(reason)::inventory_adjustment_reason, $8, $9, $10
) RETURNING *;

-- name: SetPackagedBottles :one
UPDATE packaged_inventory SET bottles_on_hand = $2, updated_at = NOW()
WHERE id = $1 RETURNING *;

-- name: SetMaterialLotQuantity :one
UPDATE material_lots SET quantity_on_hand = $2 WHERE id = $1 RETURNING *;

-- name: ListPackagedAdjustments :many
SELECT a.*, pi.lot_code, p.name AS product_name
FROM packaged_adjustments a
JOIN packaged_inventory pi ON pi.id = a.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
ORDER BY a.occurred_on DESC, a.created_at DESC
LIMIT 200;

-- Seeding a sheet: everything in scope, with what the book says now.

-- name: StockCountBulkSubjects :many
SELECT id, name, current_volume_l, current_abv_pct
FROM bulk_containers
WHERE NOT archived
  AND (sqlc.narg(location_id)::uuid IS NULL OR location_id = sqlc.narg(location_id)::uuid)
ORDER BY name;

-- name: StockCountPackagedSubjects :many
SELECT pi.id, pi.lot_code, pi.bottles_on_hand, p.name AS product_name
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id
WHERE pi.bottles_on_hand > 0
  AND (sqlc.narg(location_id)::uuid IS NULL OR pi.location_id = sqlc.narg(location_id)::uuid)
ORDER BY p.name, pi.lot_code;

-- name: StockCountMaterialSubjects :many
SELECT ml.id, ml.quantity_on_hand, ml.supplier_lot, m.name AS material_name, m.uom
FROM material_lots ml
JOIN materials m ON m.id = ml.material_id
WHERE ml.quantity_on_hand > 0
ORDER BY m.name, ml.received_at;

-- name: GetStockCountLine :one
SELECT * FROM stock_count_lines WHERE id = $1;
