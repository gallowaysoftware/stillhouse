-- name: CreateProduct :one
INSERT INTO products (
    tenant_id, name, spirit_kind, bottle_size_ml, target_abv_pct, label_notes
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: UpdateProduct :one
UPDATE products
SET name           = $2,
    spirit_kind    = $3,
    bottle_size_ml = $4,
    target_abv_pct = $5,
    label_notes    = $6
WHERE id = $1
RETURNING *;

-- name: UpdateProductSKU :one
-- The trade and label fields, set separately from the production ones.
-- Bottle size and strength change what is in the bottle; a GTIN or a
-- case configuration changes how it is sold, and the two are edited by
-- different people on different days.
UPDATE products
SET gtin                  = $2,
    cspc_code             = $3,
    bottles_per_case      = $4,
    cases_per_layer       = $5,
    layers_per_pallet     = $6,
    case_gross_weight_kg  = $7,
    common_name           = $8,
    age_statement         = $9,
    container_marking     = $10,
    allergen_statement    = $11,
    country_of_origin     = $12,
    marketing_description = $13
WHERE id = $1
RETURNING *;

-- name: SetProductArchived :one
UPDATE products SET archived = $2 WHERE id = $1 RETURNING *;

-- name: GetProduct :one
SELECT * FROM products WHERE id = $1;

-- name: ListProducts :many
SELECT * FROM products
WHERE (sqlc.arg('include_archived')::boolean OR NOT archived)
ORDER BY archived, name;
