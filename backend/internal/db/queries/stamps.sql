-- name: CreateStampOrder :one
INSERT INTO excise_stamp_orders (
    tenant_id, jurisdiction, quantity_ordered, notes
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: ReceiveStampOrder :one
UPDATE excise_stamp_orders
SET received_at       = COALESCE($2, NOW()),
    quantity_received = $3,
    serial_start      = $4,
    serial_end        = $5,
    status            = 'received'
WHERE id = $1
RETURNING *;

-- name: IncrementStampOrderApplied :one
UPDATE excise_stamp_orders
SET quantity_applied = quantity_applied + $2
WHERE id = $1
RETURNING *;

-- name: IncrementStampOrderVoided :one
UPDATE excise_stamp_orders
SET quantity_voided = quantity_voided + $2
WHERE id = $1
RETURNING *;

-- name: GetStampOrder :one
SELECT * FROM excise_stamp_orders WHERE id = $1;

-- name: ListStampOrders :many
SELECT * FROM excise_stamp_orders
WHERE (sqlc.narg('jurisdiction')::text IS NULL OR jurisdiction = sqlc.narg('jurisdiction')::text)
ORDER BY ordered_at DESC;

-- name: ListStampOrdersWithAvailable :many
SELECT *,
       (quantity_received - quantity_applied - quantity_voided)::int AS available_count
FROM excise_stamp_orders
WHERE jurisdiction = $1
  AND status = 'received'
  AND quantity_received - quantity_applied - quantity_voided > 0
ORDER BY ordered_at;

-- name: SumStampInventory :many
SELECT jurisdiction,
       SUM(quantity_received)::int AS total_received,
       SUM(quantity_applied)::int  AS total_applied,
       SUM(quantity_voided)::int   AS total_voided,
       SUM(quantity_received - quantity_applied - quantity_voided)::int AS total_on_hand
FROM excise_stamp_orders
GROUP BY jurisdiction
ORDER BY jurisdiction;
