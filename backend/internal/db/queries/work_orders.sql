-- name: NextWorkOrderNo :one
SELECT COALESCE(MAX(work_order_no), 0)::INTEGER + 1 AS next FROM work_orders;

-- name: CreateWorkOrder :one
INSERT INTO work_orders (
    tenant_id, work_order_no, kind, title, detail, assigned_to, assigned_role,
    location_id, scheduled_for, due_on, container_id, product_id, recipe_id, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: UpdateWorkOrder :one
UPDATE work_orders SET
    kind = $2, title = $3, detail = $4, assigned_to = $5, assigned_role = $6,
    location_id = $7, scheduled_for = $8, due_on = $9,
    container_id = $10, product_id = $11, recipe_id = $12
WHERE id = $1 AND status IN ('planned', 'in_progress')
RETURNING *;

-- name: SetWorkOrderStatus :one
-- started_at and completed_at are stamped by the transition rather than
-- supplied, so "when did this actually start" is a fact rather than
-- something somebody typed afterwards.
UPDATE work_orders SET
    status       = sqlc.arg(status)::work_order_status,
    started_at   = CASE WHEN sqlc.arg(status)::work_order_status = 'in_progress'
                         AND started_at IS NULL THEN NOW() ELSE started_at END,
    completed_at = CASE WHEN sqlc.arg(status)::work_order_status = 'done'
                        THEN NOW() ELSE NULL END,
    completed_by = CASE WHEN sqlc.arg(status)::work_order_status = 'done'
                        THEN sqlc.narg(completed_by)::UUID ELSE NULL END,
    cancel_reason = CASE WHEN sqlc.arg(status)::work_order_status = 'cancelled'
                         THEN sqlc.arg(cancel_reason)::TEXT ELSE cancel_reason END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: LinkWorkOrderOutput :one
-- Points a work order at what it produced. The link runs order → record,
-- so the production tables know nothing about planning.
UPDATE work_orders SET
    mash_run_id         = COALESCE(sqlc.narg(mash_run_id)::UUID, mash_run_id),
    distillation_run_id = COALESCE(sqlc.narg(distillation_run_id)::UUID, distillation_run_id),
    bottling_run_id     = COALESCE(sqlc.narg(bottling_run_id)::UUID, bottling_run_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListWorkOrders :many
SELECT w.*,
       COALESCE(u.display_name, '')  AS assigned_to_name,
       COALESCE(c.display_name, '')  AS completed_by_name,
       COALESCE(l.name, '')          AS location_name,
       COALESCE(bc.name, '')         AS container_name,
       COALESCE(p.name, '')          AS product_name,
       COALESCE(r.name, '')          AS recipe_name
FROM work_orders w
LEFT JOIN users u            ON u.id = w.assigned_to
LEFT JOIN users c            ON c.id = w.completed_by
LEFT JOIN locations l        ON l.id = w.location_id
LEFT JOIN bulk_containers bc ON bc.id = w.container_id
LEFT JOIN products p         ON p.id = w.product_id
LEFT JOIN recipes r          ON r.id = w.recipe_id
WHERE (sqlc.arg(open_only)::BOOLEAN = FALSE OR w.status IN ('planned', 'in_progress'))
  AND (sqlc.narg(assigned_to)::UUID IS NULL OR w.assigned_to = sqlc.narg(assigned_to)::UUID)
ORDER BY
    CASE w.status WHEN 'in_progress' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END,
    w.scheduled_for NULLS LAST, w.due_on NULLS LAST, w.work_order_no
LIMIT sqlc.arg(row_limit);

-- name: GetWorkOrder :one
SELECT * FROM work_orders WHERE id = $1;

-- name: AlertOverdueWorkOrders :many
-- Open work whose due date has passed. Deliberately not "scheduled for
-- the past": a job scheduled Monday and done Tuesday is normal, and a
-- system that shouts about it gets muted. A missed *due* date is a
-- commitment broken.
SELECT w.id, w.work_order_no, w.title, w.due_on,
       COALESCE(u.display_name, '') AS assigned_to_name
FROM work_orders w
LEFT JOIN users u ON u.id = w.assigned_to
WHERE w.status IN ('planned', 'in_progress')
  AND w.due_on IS NOT NULL
  AND w.due_on < sqlc.arg(today)::DATE;
