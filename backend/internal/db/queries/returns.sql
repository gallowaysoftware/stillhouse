-- name: NextPackagedReturnNo :one
SELECT COALESCE(MAX(return_no), 0) + 1 FROM packaged_returns;

-- name: CreatePackagedReturn :one
INSERT INTO packaged_returns (
    tenant_id, return_no, packaged_inventory_id, customer_id, removal_id,
    bottles, condition, returned_on, reason, credit_amount_cad,
    credit_note_no, duty_paid_cad, notes, created_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING *;

-- name: RestockFromReturn :one
-- Only a saleable return restocks; the caller decides, and passes zero for
-- one that does not. bottles_removed is reduced too, so the lot's own
-- arithmetic — packaged, removed, on hand — still ties out. Without that
-- a returned bottle would show as both removed and on hand.
UPDATE packaged_inventory
SET bottles_on_hand = bottles_on_hand + @bottles::int,
    bottles_removed = GREATEST(bottles_removed - @bottles::int, 0),
    updated_at      = NOW()
WHERE id = @packaged_inventory_id
RETURNING *;

-- name: ListPackagedReturns :many
SELECT r.*, pi.lot_code, p.name AS product_name,
       COALESCE(c.name, '')::text AS customer_name
FROM packaged_returns r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
LEFT JOIN customers c      ON c.id = r.customer_id
ORDER BY r.returned_on DESC, r.return_no DESC
LIMIT @row_limit::int;

-- name: VoidPackagedReturn :one
UPDATE packaged_returns
SET voided_at = NOW(), voided_by = $2, void_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;
