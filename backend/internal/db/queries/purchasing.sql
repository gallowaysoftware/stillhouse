-- name: CreateSupplier :one
INSERT INTO suppliers (
    tenant_id, name, account_reference, contact_name, email, phone,
    address, payment_terms_days, country, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateSupplier :one
UPDATE suppliers SET
    name = $2, account_reference = $3, contact_name = $4, email = $5,
    phone = $6, address = $7, payment_terms_days = $8, country = $9,
    notes = $10, archived_at = $11
WHERE id = $1
RETURNING *;

-- name: ListSuppliers :many
SELECT * FROM suppliers
WHERE (sqlc.arg(include_archived)::BOOLEAN OR archived_at IS NULL)
ORDER BY name;

-- name: GetSupplier :one
SELECT * FROM suppliers WHERE id = $1;

-- name: NextPurchaseOrderNo :one
SELECT COALESCE(MAX(po_no), 0)::INTEGER + 1 AS next FROM purchase_orders;

-- name: CreatePurchaseOrder :one
INSERT INTO purchase_orders (
    tenant_id, supplier_id, po_no, ordered_on, expected_on, reference, currency, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPurchaseOrder :one
SELECT * FROM purchase_orders WHERE id = $1;

-- name: ListPurchaseOrders :many
SELECT po.*, s.name AS supplier_name,
       COALESCE(SUM(l.quantity_ordered * l.unit_price), 0)::NUMERIC AS total_value,
       COUNT(l.id)::INTEGER AS line_count
FROM purchase_orders po
JOIN suppliers s ON s.id = po.supplier_id
LEFT JOIN purchase_order_lines l ON l.purchase_order_id = po.id
WHERE (sqlc.arg(open_only)::BOOLEAN = FALSE
       OR po.status IN ('draft', 'placed', 'partially_received'))
GROUP BY po.id, s.name
ORDER BY po.expected_on NULLS LAST, po.po_no DESC;

-- name: SetPurchaseOrderStatus :one
-- The status is cast explicitly at every use. Postgres cannot deduce one
-- type for a parameter that is both assigned to an enum column and
-- compared against a literal, and the error it gives (42P08, inconsistent
-- types deduced) says nothing about which parameter.
UPDATE purchase_orders
SET status        = sqlc.arg(status)::purchase_order_status,
    placed_by     = COALESCE(sqlc.narg(placed_by)::UUID, placed_by),
    placed_at     = CASE WHEN sqlc.arg(status)::purchase_order_status = 'placed'
                          AND placed_at IS NULL THEN NOW() ELSE placed_at END,
    closed_at     = CASE WHEN sqlc.arg(status)::purchase_order_status = 'closed'
                         THEN NOW() ELSE closed_at END,
    cancelled_at  = CASE WHEN sqlc.arg(status)::purchase_order_status = 'cancelled'
                         THEN NOW() ELSE cancelled_at END,
    cancel_reason = CASE WHEN sqlc.arg(status)::purchase_order_status = 'cancelled'
                         THEN sqlc.arg(cancel_reason)::TEXT ELSE cancel_reason END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AddPurchaseOrderLine :one
INSERT INTO purchase_order_lines (
    tenant_id, purchase_order_id, material_id, quantity_ordered, unit_price, uom, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeletePurchaseOrderLine :exec
-- Only reachable on a draft; the handler enforces that. A line that has
-- been ordered against is history, not a typo.
DELETE FROM purchase_order_lines WHERE id = $1;

-- name: ListPurchaseOrderLines :many
SELECT l.*, m.name AS material_name, m.uom AS material_uom
FROM purchase_order_lines l
JOIN materials m ON m.id = l.material_id
WHERE l.purchase_order_id = $1
ORDER BY m.name;

-- name: GetPurchaseOrderLineForUpdate :one
-- Locked, because two receipts against one line both read the
-- outstanding quantity, both decide there is room, and both increment —
-- the same lost update fixed for bulk containers in stage 131.
SELECT * FROM purchase_order_lines WHERE id = $1 FOR UPDATE;

-- name: IncrementPurchaseOrderLineReceived :one
UPDATE purchase_order_lines
SET quantity_received = quantity_received + $2
WHERE id = $1
RETURNING *;

-- name: PurchaseOrderOutstanding :one
-- Whether anything is still owed on this order, so the status can follow
-- the lines rather than being set by hand.
SELECT
    COUNT(*) FILTER (WHERE l.quantity_received < l.quantity_ordered)::INTEGER AS lines_outstanding,
    COUNT(*) FILTER (WHERE l.quantity_received > 0)::INTEGER                  AS lines_started
FROM purchase_order_lines l
WHERE l.purchase_order_id = $1;

-- name: CreateMaterialLotFromReceipt :one
-- The receiving path. Carries the order line, the supplier, and the
-- landed-cost components; landed_unit_cost_cad is generated from them.
-- quantity_on_hand starts equal to quantity_received: a lot that has
-- just arrived is entirely on the shelf.
INSERT INTO material_lots (
    tenant_id, material_id, supplier_lot, quantity_received, quantity_on_hand,
    received_at, notes, unit_cost_cad, purchase_order_line_id, supplier_id,
    freight_cad, import_duty_cad, handling_cad, invoice_reference
) VALUES ($1, $2, $3, $4, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: SetMaterialLotLandedCharges :one
-- Charges often arrive after the goods — a freight invoice a week later.
-- Setting them updates the landed cost of a lot already on the shelf,
-- which is correct: the cost of getting it here did not change, only
-- when we learned it.
UPDATE material_lots
SET freight_cad     = $2,
    import_duty_cad = $3,
    handling_cad    = $4
WHERE id = $1
RETURNING *;

-- name: MarkMaterialLotInvoiced :one
UPDATE material_lots
SET invoiced_at = NOW(), invoice_reference = $2
WHERE id = $1
RETURNING *;

-- name: ListGoodsReceivedNotInvoiced :many
-- What has arrived and not yet been billed. The one report a monthly
-- close actually needs out of receiving.
SELECT l.*, m.name AS material_name, COALESCE(s.name, '') AS supplier_name
FROM material_lots l
JOIN materials m       ON m.id = l.material_id
LEFT JOIN suppliers s  ON s.id = l.supplier_id
WHERE l.invoiced_at IS NULL
  AND l.unit_cost_cad IS NOT NULL
ORDER BY l.received_at;
