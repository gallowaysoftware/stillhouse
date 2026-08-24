-- name: NextConsignmentNo :one
SELECT COALESCE(MAX(consignment_no), 0) + 1 FROM consignments;

-- name: CreateConsignment :one
INSERT INTO consignments (tenant_id, consignment_no, packaged_inventory_id,
                          customer_id, bottles, sent_on, notes, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: ListConsignments :many
SELECT c.*, pi.lot_code, p.name AS product_name, cu.name AS customer_name,
       (c.bottles - c.bottles_settled - c.bottles_recalled)::int AS bottles_out
FROM consignments c
JOIN packaged_inventory pi ON pi.id = c.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
JOIN customers cu          ON cu.id = c.customer_id
ORDER BY c.sent_on DESC, c.consignment_no DESC
LIMIT @row_limit::int;

-- name: GetConsignment :one
SELECT * FROM consignments WHERE id = $1;

-- name: GetConsignmentForUpdate :one
-- Locked, because settling is a read-then-write: two people recording a
-- sell-through at once would otherwise each add to the count they read
-- and one of them would be lost.
SELECT * FROM consignments WHERE id = $1 FOR UPDATE;

-- name: SettleConsignment :one
-- The new counts and the status the caller worked out. Kept dumb on
-- purpose: the rule that a consignment closes when nothing is still out,
-- and closes as settled rather than recalled if anything sold, is
-- arithmetic worth testing without a database.
UPDATE consignments
SET bottles_settled  = @settled::int,
    bottles_recalled = @recalled::int,
    status           = @status,
    settled_on       = @settled_on,
    updated_at       = NOW()
WHERE id = @id
RETURNING *;

-- name: ConsignmentSummary :one
SELECT COUNT(*) FILTER (WHERE status = 'out')::int AS open_consignments,
       COALESCE(SUM(bottles - bottles_settled - bottles_recalled)
                FILTER (WHERE status = 'out'), 0)::int AS bottles_out
FROM consignments;
