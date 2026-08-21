-- name: CreateInventoryAdjustment :one
INSERT INTO inventory_adjustments (
    tenant_id, container_id, bulk_movement_id, reason, explanation,
    book_volume_l, book_abv_pct, book_laa,
    counted_volume_l, counted_abv_pct, counted_laa,
    delta_laa, delta_volume_l,
    temperature_c, observed_volume_l, observed_density_kg_m3,
    volume_factor_c, strength_source,
    volume_instrument_id, strength_instrument_id, temperature_instrument_id,
    adjusted_by, notes, occurred_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24
) RETURNING *;

-- name: ListInventoryAdjustments :many
-- Filters are all optional so one query backs both the container's own
-- history and the period review behind B266 line D.
SELECT ia.*,
       bc.name AS container_name,
       u.display_name AS adjusted_by_name
FROM inventory_adjustments ia
JOIN bulk_containers bc ON bc.id = ia.container_id
JOIN users u            ON u.id  = ia.adjusted_by
WHERE (sqlc.narg('container_id')::uuid IS NULL OR ia.container_id = sqlc.narg('container_id')::uuid)
  AND (sqlc.narg('period_start')::date IS NULL OR ia.occurred_at >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR ia.occurred_at <  sqlc.narg('period_end')::date + 1)
ORDER BY ia.occurred_at DESC, ia.created_at DESC
LIMIT 500;

-- name: SumInventoryAdjustmentsInPeriod :one
-- Line D. Both directions are reported, not just the net: a period that
-- found 3 LAA in one tank and lost 3 in another nets to zero, and a line
-- showing only the net would say nothing happened.
SELECT COALESCE(SUM(delta_laa), 0)::double precision AS net_laa,
       COALESCE(SUM(delta_laa) FILTER (WHERE delta_laa > 0), 0)::double precision AS increase_laa,
       COALESCE(-SUM(delta_laa) FILTER (WHERE delta_laa < 0), 0)::double precision AS decrease_laa,
       COUNT(*)::int AS adjustment_count
FROM inventory_adjustments
WHERE occurred_at >= $1 AND occurred_at < $2;
