-- name: NextRemovalNo :one
SELECT COALESCE(MAX(removal_no), 0)::int + 1 AS next FROM packaging_removals;

-- name: CreateRemoval :one
INSERT INTO packaging_removals (
    tenant_id, removal_no, packaged_inventory_id, removal_date, bottles_removed,
    destination_kind, destination_name, reference,
    bottle_size_ml, bottle_abv_pct, total_litres, total_laa,
    duty_rate_per_laa, duty_amount_cad, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
) RETURNING *;

-- name: DecrementPackagedOnHand :one
UPDATE packaged_inventory
SET bottles_on_hand = bottles_on_hand - $2,
    bottles_removed = bottles_removed + $2
WHERE id = $1
RETURNING *;

-- name: ListRemovals :many
SELECT pr.*,
       pi.lot_code        AS lot_code,
       pi.jurisdiction    AS jurisdiction,
       p.name             AS product_name
FROM packaging_removals pr
JOIN packaged_inventory pi ON pi.id = pr.packaged_inventory_id
JOIN products p             ON p.id = pi.product_id
WHERE (sqlc.narg('period_start')::date IS NULL OR pr.removal_date >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR pr.removal_date <= sqlc.narg('period_end')::date)
ORDER BY pr.removal_date DESC, pr.removal_no DESC
LIMIT $1 OFFSET $2;

-- name: CountRemovals :one
SELECT COUNT(*)::int AS total
FROM packaging_removals
WHERE (sqlc.narg('period_start')::date IS NULL OR removal_date >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR removal_date <= sqlc.narg('period_end')::date);

-- name: GetRemoval :one
SELECT * FROM packaging_removals WHERE id = $1;

-- name: VoidRemoval :one
UPDATE packaging_removals
SET voided_at = NOW(),
    voided_by = $2,
    voided_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;

-- name: IncrementPackagedOnHand :one
UPDATE packaged_inventory
SET bottles_on_hand = bottles_on_hand + $2,
    bottles_removed = bottles_removed - $2
WHERE id = $1
RETURNING *;
