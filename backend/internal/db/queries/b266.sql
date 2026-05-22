-- name: GetB266Period :one
SELECT * FROM b266_periods WHERE id = $1;

-- name: GetB266PeriodByDates :one
SELECT * FROM b266_periods
WHERE period_start = $1 AND period_end = $2;

-- name: ListB266Periods :many
SELECT * FROM b266_periods
ORDER BY period_start DESC;

-- name: UpsertB266PeriodDraft :one
INSERT INTO b266_periods (tenant_id, period_start, period_end, status)
VALUES ($1, $2, $3, 'draft')
ON CONFLICT (tenant_id, period_start, period_end) DO UPDATE
SET updated_at = NOW()
RETURNING *;

-- name: SubmitB266Period :one
UPDATE b266_periods
SET status       = 'submitted',
    snapshot     = $2,
    submitted_at = NOW(),
    submitted_by = $3
WHERE id = $1
  AND status = 'draft'
RETURNING *;

-- Aggregation queries for generating B266 sections.

-- name: SumBulkMovementsByReason :many
SELECT reason::text AS reason,
       SUM(laa)::double precision AS total_laa,
       COUNT(*)::int AS count
FROM bulk_movements
WHERE occurred_at >= $1 AND occurred_at < $2
GROUP BY reason
ORDER BY reason;

-- SumBottlingRunsInPeriod excludes voided runs; voided runs are reversed in
-- packaged_inventory and bulk separately, so they shouldn't count toward
-- either the packaging or production lines on B266.
-- name: SumBottlingRunsInPeriod :one
SELECT COALESCE(SUM(tank_gauge_laa), 0)::double precision AS total_laa,
       COUNT(*)::int AS run_count,
       COALESCE(SUM(bottle_count), 0)::int AS total_bottles
FROM bottling_runs
WHERE bottling_date >= $1 AND bottling_date < $2
  AND voided_at IS NULL;

-- name: SumRemovalsInPeriod :one
SELECT COALESCE(SUM(total_laa), 0)::double precision      AS total_laa,
       COALESCE(SUM(duty_amount_cad), 0)::double precision AS total_duty,
       COALESCE(SUM(bottles_removed), 0)::int             AS total_bottles,
       COUNT(*)::int                                      AS removal_count
FROM packaging_removals
WHERE removal_date >= $1 AND removal_date < $2
  AND voided_at IS NULL;

-- name: SumBulkOnHandAsOfDate :one
-- LAA on hand right now (we don't have point-in-time snapshots; B266 generated
-- for a closed period uses current values, which is fine if generated promptly
-- after period close).
SELECT COALESCE(SUM(current_laa), 0)::double precision AS total_laa
FROM bulk_containers
WHERE NOT archived;

-- name: SumPackagedOnHandLAA :one
-- Approximate packaged LAA on hand: bottles × bottle_size × target_abv / 100 / 1000.
SELECT COALESCE(SUM(pi.bottles_on_hand * p.bottle_size_ml * p.target_abv_pct / 100000.0), 0)::double precision AS total_laa,
       COALESCE(SUM(pi.bottles_on_hand), 0)::int AS total_bottles
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id;
