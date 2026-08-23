-- name: DemandByProduct :many
-- What is actually owed, against what is actually here.
--
-- Demand is confirmed, unshipped order lines — real commitments to real
-- customers, not a statistical forecast. Stillhouse has no forecast and
-- says so rather than producing one; see PLAN F7.
--
-- Supply is bottles on hand less what is already picked onto open
-- shipments, and less what other confirmed orders have spoken for, so two
-- products competing for the same stock do not both look satisfiable.
SELECT p.id AS product_id,
       p.name AS product_name,
       p.bottle_size_ml,
       p.target_abv_pct,
       COALESCE(SUM(l.bottles_ordered - l.bottles_shipped), 0)::int AS bottles_owed,
       MIN(so.required_by)::date AS earliest_required,
       COALESCE((
           SELECT SUM(pi.bottles_on_hand)
           FROM packaged_inventory pi
           WHERE pi.product_id = p.id
             AND pi.owner_customer_id IS NULL
             AND pi.held_at IS NULL
       ), 0)::int AS bottles_on_hand,
       COALESCE((
           SELECT SUM(sl.bottles)
           FROM shipment_lines sl
           JOIN shipments s          ON s.id = sl.shipment_id
           JOIN packaged_inventory q ON q.id = sl.packaged_inventory_id
           WHERE q.product_id = p.id AND s.status = 'picking'
       ), 0)::int AS bottles_picked
FROM sales_order_lines l
JOIN sales_orders so ON so.id = l.sales_order_id
JOIN products p      ON p.id = l.product_id
WHERE so.status IN ('confirmed', 'partially_shipped')
  AND l.bottles_shipped < l.bottles_ordered
GROUP BY p.id, p.name, p.bottle_size_ml, p.target_abv_pct
ORDER BY MIN(so.required_by) NULLS LAST, p.name;

-- name: ScheduledWorkByEquipment :many
-- What is already on the board, per piece of plant, in a window.
--
-- Only work with a scheduled date and a piece of equipment named. Work
-- with neither cannot be planned against a capacity, and counting it
-- would overstate how full the week is.
SELECT e.id AS equipment_id, e.name AS equipment_name, e.kind,
       e.typical_run_hours, e.status,
       w.id AS work_order_id, w.work_order_no, w.title,
       w.scheduled_for, w.due_on, w.status AS work_status
FROM work_orders w
JOIN equipment e ON e.id = w.equipment_id
WHERE w.scheduled_for IS NOT NULL
  AND w.scheduled_for >= sqlc.arg(from_date)::date
  AND w.scheduled_for <= sqlc.arg(to_date)::date
  AND w.status NOT IN ('done', 'cancelled')
ORDER BY e.name, w.scheduled_for;

-- name: PlannableEquipment :many
-- Plant that can actually be planned against: in service, with a
-- capacity and a typical run time recorded. Everything else is returned
-- too, with the reason it cannot, because an empty schedule and a
-- schedule that silently dropped half the plant look identical.
SELECT e.id, e.name, e.kind, e.status, e.capacity_l, e.typical_run_hours,
       COALESCE(obs.median_hours, 0)::double precision AS observed_median_hours,
       COALESCE(obs.n, 0)::int AS observed_runs
FROM equipment e
LEFT JOIN LATERAL (
    SELECT COUNT(*) AS n,
           PERCENTILE_CONT(0.5) WITHIN GROUP (
               ORDER BY EXTRACT(EPOCH FROM (w.completed_at - w.started_at)) / 3600.0
           ) AS median_hours
    FROM work_orders w
    WHERE w.equipment_id = e.id
      AND w.started_at IS NOT NULL
      AND w.completed_at IS NOT NULL
) obs ON TRUE
WHERE e.status <> 'retired'
ORDER BY e.kind, e.name;

-- name: BulkAvailableForBottling :one
-- Alcohol that could actually be bottled: ours, here, and not in a cask
-- somebody else owns. Barrels included, because a dumped cask is exactly
-- what a bottling run draws from.
SELECT COALESCE(SUM(current_laa), 0)::double precision AS available_laa
FROM bulk_containers
WHERE NOT archived
  AND owner_customer_id IS NULL
  AND possession = 'held'
  AND current_laa > 0;
