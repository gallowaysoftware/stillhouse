-- name: MonthlyRemovalsByProduct :many
-- Actual demand, by product by month: what left duty-paid. This is the
-- only history a forecast is allowed to be built from — a sales order is
-- a promise and a removal is what happened.
--
-- Voided removals are excluded: the stock did not leave, so it was not
-- demand.
SELECT pi.product_id,
       p.name AS product_name,
       date_trunc('month', r.removal_date)::date AS month,
       SUM(r.bottles_removed)::int AS bottles
FROM packaging_removals r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
WHERE r.voided_at IS NULL
  AND r.destination_kind = 'duty_paid_customer'
  AND r.removal_date >= @since::date
  AND r.removal_date <  date_trunc('month', CURRENT_DATE)::date
GROUP BY pi.product_id, p.name, date_trunc('month', r.removal_date)
ORDER BY pi.product_id, month;

-- name: GetForecastSettings :one
SELECT forecast_method, forecast_trailing_months FROM tenants WHERE id = $1;

-- name: SetForecastSettings :one
UPDATE tenants
SET forecast_method = $2, forecast_trailing_months = $3, updated_at = NOW()
WHERE id = $1
RETURNING forecast_method, forecast_trailing_months;

-- name: UpsertDemandForecast :one
INSERT INTO demand_forecasts (tenant_id, product_id, period_start, period_end,
                              bottles, reason, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (tenant_id, product_id, period_start, period_end) DO UPDATE
SET bottles = EXCLUDED.bottles, reason = EXCLUDED.reason, updated_at = NOW()
RETURNING *;

-- name: ListDemandForecastsForPeriod :many
SELECT f.*, p.name AS product_name
FROM demand_forecasts f
JOIN products p ON p.id = f.product_id
WHERE f.period_start = @period_start::date AND f.period_end = @period_end::date;

-- name: DeleteDemandForecast :exec
DELETE FROM demand_forecasts WHERE id = $1;

-- name: SetProductRecipe :one
UPDATE products SET recipe_version_id = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: RecipeForProduct :one
-- The bill a product is planned from, with the efficiencies that turn it
-- into alcohol. Refuses (no rows) when the operator has not said which
-- recipe a product comes from — see 000068.
SELECT rv.id, rv.mash_efficiency_fraction, rv.ferment_efficiency_fraction,
       rv.distillation_recovery_fraction, r.name AS recipe_name, rv.version_no
FROM products p
JOIN recipe_versions rv ON rv.id = p.recipe_version_id
JOIN recipes r          ON r.id = rv.recipe_id
WHERE p.id = $1;

-- name: RecipeIngredientsForProjection :many
SELECT ri.quantity, ri.uom, m.name AS material_name,
       m.extract_fraction
FROM recipe_ingredients ri
JOIN materials m ON m.id = ri.material_id
WHERE ri.recipe_version_id = $1
ORDER BY ri.sort_order;

-- name: AlcoholOnHandForPlanning :one
-- What could actually be bottled next month, split by whether it is ready.
--
-- A cask still maturing is alcohol the distillery owns and cannot use for
-- next month's orders, so adding it to the free figure would say a
-- shortfall is covered when it is not. Barrels are counted separately for
-- exactly that reason.
SELECT COALESCE(SUM(current_laa) FILTER (WHERE kind <> 'barrel'), 0)::double precision AS free_laa,
       COALESCE(SUM(current_laa) FILTER (WHERE kind = 'barrel'), 0)::double precision AS maturing_laa
FROM bulk_containers
WHERE NOT archived
  AND owner_customer_id IS NULL
  AND possession = 'held'
  AND current_laa > 0;
