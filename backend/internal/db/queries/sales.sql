-- name: NextSalesOrderNo :one
SELECT COALESCE(MAX(order_no), 0)::INTEGER + 1 AS next FROM sales_orders;

-- name: CreateSalesOrder :one
INSERT INTO sales_orders (
    tenant_id, customer_id, order_no, ordered_on, required_by,
    customer_reference, price_list_id, location_id, notes, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetSalesOrder :one
SELECT * FROM sales_orders WHERE id = $1;

-- name: ListSalesOrders :many
SELECT so.*, c.name AS customer_name,
       COUNT(l.id)::INTEGER AS line_count,
       COALESCE(SUM(l.bottles_ordered), 0)::INTEGER AS bottles_ordered,
       COALESCE(SUM(l.bottles_shipped), 0)::INTEGER AS bottles_shipped
FROM sales_orders so
JOIN customers c ON c.id = so.customer_id
LEFT JOIN sales_order_lines l ON l.sales_order_id = so.id
WHERE (sqlc.arg(open_only)::BOOLEAN = FALSE
       OR so.status IN ('draft', 'confirmed', 'partially_shipped'))
GROUP BY so.id, c.name
ORDER BY so.required_by NULLS LAST, so.order_no DESC;

-- name: SetSalesOrderStatus :one
UPDATE sales_orders SET
    status       = sqlc.arg(status)::sales_order_status,
    confirmed_at = CASE WHEN sqlc.arg(status)::sales_order_status = 'confirmed'
                         AND confirmed_at IS NULL THEN NOW() ELSE confirmed_at END,
    confirmed_by = CASE WHEN sqlc.arg(status)::sales_order_status = 'confirmed'
                         AND confirmed_by IS NULL THEN sqlc.narg(actor)::UUID ELSE confirmed_by END,
    cancelled_at = CASE WHEN sqlc.arg(status)::sales_order_status = 'cancelled'
                        THEN NOW() ELSE cancelled_at END,
    cancel_reason = CASE WHEN sqlc.arg(status)::sales_order_status = 'cancelled'
                         THEN sqlc.arg(cancel_reason)::TEXT ELSE cancel_reason END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AddSalesOrderLine :one
INSERT INTO sales_order_lines (
    tenant_id, sales_order_id, product_id, bottles_ordered, unit_price, notes
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteSalesOrderLine :exec
DELETE FROM sales_order_lines WHERE id = $1;

-- name: ListSalesOrderLines :many
SELECT l.*, p.name AS product_name, p.bottle_size_ml, p.target_abv_pct
FROM sales_order_lines l
JOIN products p ON p.id = l.product_id
WHERE l.sales_order_id = $1
ORDER BY p.name;

-- name: GetSalesOrderLineForUpdate :one
SELECT * FROM sales_order_lines WHERE id = $1 FOR UPDATE;

-- name: IncrementSalesOrderLineShipped :one
UPDATE sales_order_lines
SET bottles_shipped = bottles_shipped + $2
WHERE id = $1
RETURNING *;

-- name: SalesOrderOutstanding :one
SELECT
    COUNT(*) FILTER (WHERE l.bottles_shipped < l.bottles_ordered)::INTEGER AS lines_outstanding,
    COUNT(*) FILTER (WHERE l.bottles_shipped > 0)::INTEGER                 AS lines_started
FROM sales_order_lines l
WHERE l.sales_order_id = $1;

-- name: ReservedBottlesForProduct :one
-- What is spoken for on confirmed, unshipped order lines. Deliberately a
-- read rather than a decrement: the alcohol has not moved, and a B266
-- built on promises rather than movements would be wrong. This tells the
-- screen; it does not tell the ledger.
SELECT COALESCE(SUM(l.bottles_ordered - l.bottles_shipped), 0)::INTEGER AS reserved
FROM sales_order_lines l
JOIN sales_orders so ON so.id = l.sales_order_id
WHERE l.product_id = $1
  AND so.status IN ('confirmed', 'partially_shipped')
  AND l.bottles_shipped < l.bottles_ordered;

-- name: NextShipmentNo :one
SELECT COALESCE(MAX(shipment_no), 0)::INTEGER + 1 AS next FROM shipments;

-- name: CreateShipment :one
INSERT INTO shipments (
    tenant_id, sales_order_id, customer_id, shipment_no, location_id,
    ship_date, carrier, tracking_ref, bol_reference, notes, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetShipment :one
SELECT * FROM shipments WHERE id = $1;

-- name: GetShipmentForUpdate :one
-- Locked before shipping: shipping twice would write two sets of
-- removals against one pallet and double the duty on the return.
SELECT * FROM shipments WHERE id = $1 FOR UPDATE;

-- name: ListShipments :many
SELECT s.*, c.name AS customer_name,
       COALESCE(so.order_no, 0)::INTEGER AS order_no,
       COUNT(sl.id)::INTEGER AS line_count,
       COALESCE(SUM(sl.bottles), 0)::INTEGER AS bottles
FROM shipments s
JOIN customers c ON c.id = s.customer_id
LEFT JOIN sales_orders so ON so.id = s.sales_order_id
LEFT JOIN shipment_lines sl ON sl.shipment_id = s.id
WHERE (sqlc.arg(open_only)::BOOLEAN = FALSE OR s.status = 'picking')
GROUP BY s.id, c.name, so.order_no
ORDER BY s.ship_date NULLS LAST, s.shipment_no DESC;

-- name: MarkShipmentShipped :one
UPDATE shipments
SET status = 'shipped', shipped_at = NOW(), shipped_by = $2,
    ship_date = COALESCE(ship_date, CURRENT_DATE)
WHERE id = $1 AND status = 'picking'
RETURNING *;

-- name: CancelShipment :one
UPDATE shipments
SET status = 'cancelled', cancelled_at = NOW(), cancel_reason = $2
WHERE id = $1 AND status = 'picking'
RETURNING *;

-- name: AddShipmentLine :one
INSERT INTO shipment_lines (
    tenant_id, shipment_id, sales_order_line_id, packaged_inventory_id, bottles, notes
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteShipmentLine :exec
DELETE FROM shipment_lines WHERE id = $1;

-- name: ListShipmentLines :many
SELECT sl.*, pi.lot_code, pi.jurisdiction, pi.bottles_on_hand,
       p.name AS product_name, p.bottle_size_ml, p.target_abv_pct,
       pi.released_at, pi.held_at
FROM shipment_lines sl
JOIN packaged_inventory pi ON pi.id = sl.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
WHERE sl.shipment_id = $1
ORDER BY p.name, pi.lot_code;

-- name: LinkShipmentLineRemoval :exec
UPDATE shipment_lines SET packaging_removal_id = $2 WHERE id = $1;

-- name: SetRemovalShipment :exec
UPDATE packaging_removals SET shipment_id = $2 WHERE id = $1;

-- name: GetPriceListEntryForProduct :one
SELECT * FROM price_list_entries WHERE price_list_id = $1 AND product_id = $2;

-- name: GetShipmentLine :one
SELECT * FROM shipment_lines WHERE id = $1;

-- name: SetShipmentShipDate :one
UPDATE shipments SET ship_date = $2 WHERE id = $1 RETURNING *;

-- name: BottlesPickedFromLot :one
-- Bottles spoken for on shipments that are still being picked. Picking
-- does not decrement the lot — stock leaves once, at the shipment — so
-- this is what stands between two pickers promising the same bottles.
SELECT COALESCE(SUM(sl.bottles), 0)::INTEGER AS picked
FROM shipment_lines sl
JOIN shipments s ON s.id = sl.shipment_id
WHERE sl.packaged_inventory_id = $1
  AND s.status = 'picking';

-- name: ShipmentLotBreakdown :many
-- What a picker is about to put on the return, per lot, before they
-- commit to it.
SELECT sl.bottles, p.bottle_size_ml, p.target_abv_pct
FROM shipment_lines sl
JOIN packaged_inventory pi ON pi.id = sl.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
WHERE sl.shipment_id = $1;

-- name: StockCommitments :many
-- On hand, spoken for, picked and free — per product.
--
-- The three middle columns are the reason reservation can stay soft. The
-- alcohol has not moved, so nothing is decremented and the B266 is built
-- from movements only; but the screen still tells an operator that the
-- cases in front of them are already promised to somebody.
SELECT
    p.id   AS product_id,
    p.name AS product_name,
    p.bottle_size_ml,
    p.target_abv_pct,
    COALESCE(SUM(pi.bottles_on_hand), 0)::INTEGER AS bottles_on_hand,
    COALESCE((
        SELECT SUM(l.bottles_ordered - l.bottles_shipped)
        FROM sales_order_lines l
        JOIN sales_orders so ON so.id = l.sales_order_id
        WHERE l.product_id = p.id
          AND so.status IN ('confirmed', 'partially_shipped')
          AND l.bottles_shipped < l.bottles_ordered
    ), 0)::INTEGER AS bottles_reserved,
    COALESCE((
        SELECT SUM(sl.bottles)
        FROM shipment_lines sl
        JOIN shipments s          ON s.id = sl.shipment_id
        JOIN packaged_inventory q ON q.id = sl.packaged_inventory_id
        WHERE q.product_id = p.id
          AND s.status = 'picking'
    ), 0)::INTEGER AS bottles_picked
FROM products p
LEFT JOIN packaged_inventory pi ON pi.product_id = p.id
GROUP BY p.id, p.name, p.bottle_size_ml, p.target_abv_pct
HAVING COALESCE(SUM(pi.bottles_on_hand), 0) > 0
    OR COALESCE((
        SELECT SUM(l.bottles_ordered - l.bottles_shipped)
        FROM sales_order_lines l
        JOIN sales_orders so ON so.id = l.sales_order_id
        WHERE l.product_id = p.id
          AND so.status IN ('confirmed', 'partially_shipped')
          AND l.bottles_shipped < l.bottles_ordered
    ), 0) > 0
ORDER BY p.name;
