-- name: NextRemovalNo :one
SELECT COALESCE(MAX(removal_no), 0)::int + 1 AS next FROM packaging_removals;

-- name: CreateRemoval :one
INSERT INTO packaging_removals (
    tenant_id, removal_no, packaged_inventory_id, removal_date, bottles_removed,
    destination_kind, destination_name, reference,
    bottle_size_ml, bottle_abv_pct, total_litres, total_laa,
    duty_rate_per_laa, duty_amount_cad, notes, customer_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
) RETURNING *;

-- name: GetPackagedInventoryForUpdate :one
-- Read a lot's bottle count with the intent to change it. Without the
-- lock, two removals against the same lot both read the same on-hand
-- count, both pass the "enough bottles?" check in Go, and both decrement
-- — the same lost-update shape fixed for bulk containers in stage 131.
-- The CHECK (bottles_on_hand >= 0) stops the data going negative, so the
-- loser got an opaque error instead of a wrong number; the lock is what
-- makes the check in front of it mean something.
SELECT * FROM packaged_inventory WHERE id = $1 FOR UPDATE;

-- name: DecrementPackagedOnHand :one
-- The `bottles_on_hand >= $2` guard is belt to the lock's braces: if a
-- caller ever reaches here without having taken the row lock, this
-- returns no rows rather than tripping the table CHECK, and the caller
-- turns that into "someone else took those bottles" instead of a 500.
UPDATE packaged_inventory
SET bottles_on_hand = bottles_on_hand - $2,
    bottles_removed = bottles_removed + $2
WHERE id = $1 AND bottles_on_hand >= $2
RETURNING *;

-- name: ListRemovals :many
SELECT pr.*,
       pi.lot_code        AS lot_code,
       pi.jurisdiction    AS jurisdiction,
       p.name             AS product_name,
       COALESCE(c.name, '') AS customer_name
FROM packaging_removals pr
JOIN packaged_inventory pi ON pi.id = pr.packaged_inventory_id
JOIN products p             ON p.id = pi.product_id
LEFT JOIN customers c       ON c.id = pr.customer_id
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
