-- name: CreateCustomer :one
INSERT INTO customers (
    tenant_id, name, kind, jurisdiction, default_destination_kind,
    licence_number, account_reference, contact_name, email, phone,
    address, payment_terms_days, notes, price_list_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: UpdateCustomer :one
UPDATE customers SET
    name = $2, kind = $3, jurisdiction = $4, default_destination_kind = $5,
    licence_number = $6, account_reference = $7, contact_name = $8,
    email = $9, phone = $10, address = $11, payment_terms_days = $12,
    notes = $13, price_list_id = $14
WHERE id = $1
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customers WHERE id = $1;

-- name: ListCustomers :many
-- Archived customers are hidden by default but never deleted: a removal
-- points at one, and the trail behind a filed return has to stay
-- resolvable years later.
SELECT c.*, COALESCE(p.name, '') AS price_list_name
FROM customers c
LEFT JOIN price_lists p ON p.id = c.price_list_id
WHERE (sqlc.arg(include_archived)::BOOLEAN OR c.archived_at IS NULL)
  AND (sqlc.arg(kind)::TEXT = '' OR c.kind::TEXT = sqlc.arg(kind)::TEXT)
ORDER BY c.name;

-- name: SetCustomerArchived :one
UPDATE customers
SET archived_at = CASE WHEN sqlc.arg(archived)::BOOLEAN THEN NOW() ELSE NULL END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CustomerRemovalTotals :one
-- What this buyer has actually taken. Voided removals are excluded —
-- they were withdrawn, and counting them would overstate what left.
SELECT
    COUNT(*)::INTEGER                          AS removal_count,
    COALESCE(SUM(bottles_removed), 0)::INTEGER AS bottles_removed,
    COALESCE(SUM(total_laa), 0)::DOUBLE PRECISION       AS total_laa,
    COALESCE(SUM(duty_amount_cad), 0)::DOUBLE PRECISION AS duty_charged_cad
FROM packaging_removals
WHERE customer_id = $1 AND voided_at IS NULL;

-- name: CreatePriceList :one
INSERT INTO price_lists (
    tenant_id, name, channel, jurisdiction, currency,
    effective_from, effective_to, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPriceList :one
SELECT * FROM price_lists WHERE id = $1;

-- name: ListPriceLists :many
-- as_of empty means every list; otherwise only those in force that day.
SELECT * FROM price_lists
WHERE (sqlc.narg(as_of)::DATE IS NULL
       OR (effective_from <= sqlc.narg(as_of)::DATE
           AND (effective_to IS NULL OR effective_to >= sqlc.narg(as_of)::DATE)))
ORDER BY effective_from DESC, name;

-- name: ListPriceListEntries :many
SELECT e.*, p.name AS product_name, p.bottle_size_ml
FROM price_list_entries e
JOIN products p ON p.id = e.product_id
WHERE e.price_list_id = $1
ORDER BY p.name;

-- name: UpsertPriceListEntry :one
INSERT INTO price_list_entries (tenant_id, price_list_id, product_id, unit_price, case_size)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (price_list_id, product_id) DO UPDATE
SET unit_price = EXCLUDED.unit_price,
    case_size  = EXCLUDED.case_size
RETURNING *;

-- name: DeletePriceListEntry :exec
DELETE FROM price_list_entries WHERE price_list_id = $1 AND product_id = $2;
