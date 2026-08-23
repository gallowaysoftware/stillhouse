-- name: NextMarkedContainerNo :one
SELECT COALESCE(MAX(container_no), 0)::INTEGER + 1 AS next FROM marked_special_containers;

-- name: CreateMarkedContainer :one
INSERT INTO marked_special_containers (
    tenant_id, container_no, mark, capacity_l, product_id, description,
    source_container_id, volume_l, abv_pct, laa, filled_on, filled_by,
    bulk_movement_id, duty_rate_per_laa, duty_amount_cad, duty_rate_source,
    notes, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
) RETURNING *;

-- name: GetMarkedContainer :one
SELECT m.*, COALESCE(p.name, '') AS product_name,
       COALESCE(b.name, '') AS source_container_name
FROM marked_special_containers m
LEFT JOIN products p        ON p.id = m.product_id
LEFT JOIN bulk_containers b ON b.id = m.source_container_id
WHERE m.id = $1;

-- name: GetMarkedContainerForUpdate :one
SELECT * FROM marked_special_containers WHERE id = $1 FOR UPDATE;

-- name: ListMarkedContainers :many
SELECT m.*, COALESCE(p.name, '') AS product_name,
       COALESCE(b.name, '') AS source_container_name
FROM marked_special_containers m
LEFT JOIN products p        ON p.id = m.product_id
LEFT JOIN bulk_containers b ON b.id = m.source_container_id
WHERE (sqlc.arg(on_hand_only)::boolean = FALSE OR m.status = 'marked')
ORDER BY m.status, m.container_no DESC;

-- name: SetMarkedContainerStatus :one
UPDATE marked_special_containers
SET status = sqlc.arg(status)::marked_container_status, updated_at = NOW()
WHERE id = sqlc.arg(id) AND status = 'marked'
RETURNING *;

-- name: UnmarkMarkedContainer :one
-- s.156: unmarked, and its contents returned to bulk. A movement in the
-- ledger, not a correction — the alcohol really did go back.
UPDATE marked_special_containers
SET status             = 'unmarked',
    unmarked_on        = sqlc.arg(unmarked_on)::date,
    unmarked_reason    = sqlc.arg(unmarked_reason)::text,
    unmark_movement_id = sqlc.narg(unmark_movement_id)::uuid,
    updated_at         = NOW()
WHERE id = sqlc.arg(id) AND status = 'marked'
RETURNING *;

-- name: NextMarkedDeliveryNo :one
SELECT COALESCE(MAX(delivery_no), 0)::INTEGER + 1 AS next FROM marked_container_deliveries;

-- name: CreateMarkedDelivery :one
INSERT INTO marked_container_deliveries (
    tenant_id, delivery_no, container_id, delivery_date, customer_id,
    destination_name, reference, volume_l, abv_pct, laa,
    duty_rate_per_laa, duty_amount_cad, notes, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: ListMarkedDeliveries :many
SELECT d.*, m.container_no, m.mark, COALESCE(c.name, '') AS customer_name
FROM marked_container_deliveries d
JOIN marked_special_containers m ON m.id = d.container_id
LEFT JOIN customers c            ON c.id = d.customer_id
WHERE d.voided_at IS NULL
ORDER BY d.delivery_date DESC, d.delivery_no DESC;

-- name: VoidMarkedDelivery :one
UPDATE marked_container_deliveries
SET voided_at = NOW(), void_reason = $2
WHERE id = $1 AND voided_at IS NULL
RETURNING *;

-- name: SumMarkedContainersPackagedInPeriod :one
-- The third column of the B266 packaging split: what was packaged into
-- marked special containers rather than into bottles. Unmarked containers
-- are excluded — under s.156 their contents went back to bulk, and the
-- return should not say they were packaged and then say nothing about
-- their coming back.
SELECT COALESCE(SUM(laa), 0)::double precision AS total_laa,
       COALESCE(SUM(volume_l), 0)::double precision AS total_litres,
       COUNT(*)::int AS container_count,
       COALESCE(SUM(duty_amount_cad) FILTER (WHERE duty_amount_cad IS NOT NULL),
                0)::double precision AS duty_cad
FROM marked_special_containers
WHERE filled_on >= sqlc.arg(period_start)::date
  AND filled_on <  sqlc.arg(period_end)::date
  AND status <> 'unmarked';

-- name: SumMarkedDeliveriesInPeriod :one
SELECT COALESCE(SUM(laa), 0)::double precision AS total_laa,
       COALESCE(SUM(volume_l), 0)::double precision AS total_litres,
       COALESCE(SUM(duty_amount_cad), 0)::double precision AS duty_cad,
       COUNT(*)::int AS delivery_count
FROM marked_container_deliveries
WHERE voided_at IS NULL
  AND delivery_date >= sqlc.arg(period_start)::date
  AND delivery_date <  sqlc.arg(period_end)::date;

-- name: SetMarkedContainerStatusForce :one
-- Unconditional, unlike SetMarkedContainerStatus, which only advances a
-- container that is still on the premises. Voiding a delivery has to be
-- able to bring one back.
UPDATE marked_special_containers
SET status = sqlc.arg(status)::marked_container_status, updated_at = NOW()
WHERE id = sqlc.arg(id)
RETURNING *;
