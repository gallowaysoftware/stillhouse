-- A label code carries the first 64 bits of a row's id, which is the
-- first 16 characters of its hex form. Matching on a prefix rather than a
-- whole id is why every one of these returns a set: two rows sharing a
-- prefix is astronomically unlikely and is still an ambiguity to report
-- rather than a coin to flip. See internal/labelcode.

-- name: FindBulkContainersByIDPrefix :many
SELECT bc.*, COALESCE(own.name, '') AS owner_name
FROM bulk_containers bc
LEFT JOIN customers own ON own.id = bc.owner_customer_id
WHERE substr(replace(bc.id::text, '-', ''), 1, 16) = sqlc.arg(prefix)::text;

-- name: FindPackagedInventoryByIDPrefix :many
SELECT pi.*, p.name AS product_name
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id
WHERE substr(replace(pi.id::text, '-', ''), 1, 16) = sqlc.arg(prefix)::text;

-- name: FindShipmentsByIDPrefix :many
SELECT s.*, c.name AS customer_name
FROM shipments s
JOIN customers c ON c.id = s.customer_id
WHERE substr(replace(s.id::text, '-', ''), 1, 16) = sqlc.arg(prefix)::text;

-- name: FindProductsByIDPrefix :many
SELECT * FROM products
WHERE substr(replace(id::text, '-', ''), 1, 16) = sqlc.arg(prefix)::text;

-- The other half of scanning: what an operator types, or what is printed
-- on a case by somebody other than us. A retail barcode is a GTIN, and a
-- lot code is what is on the bottling record.

-- name: FindPackagedInventoryByLotCode :many
SELECT pi.*, p.name AS product_name
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id
WHERE upper(pi.lot_code) = upper(sqlc.arg(code)::text);

-- name: FindProductByGTIN :many
SELECT * FROM products WHERE gtin = sqlc.arg(gtin)::text;

-- name: FindBulkContainersByName :many
SELECT bc.*, COALESCE(own.name, '') AS owner_name
FROM bulk_containers bc
LEFT JOIN customers own ON own.id = bc.owner_customer_id
WHERE upper(bc.name) = upper(sqlc.arg(name)::text)
   OR upper(COALESCE(
        (SELECT ba.serial_burnin FROM barrel_attributes ba
          WHERE ba.container_id = bc.id), '')) = upper(sqlc.arg(name)::text);
