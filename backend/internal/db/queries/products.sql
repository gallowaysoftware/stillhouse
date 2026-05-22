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

-- name: SetProductArchived :one
UPDATE products SET archived = $2 WHERE id = $1 RETURNING *;

-- name: GetProduct :one
SELECT * FROM products WHERE id = $1;

-- name: ListProducts :many
SELECT * FROM products
WHERE (sqlc.arg('include_archived')::boolean OR NOT archived)
ORDER BY archived, name;
