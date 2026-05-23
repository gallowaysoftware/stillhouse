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

-- name: ReopenB266Period :one
-- Flips a submitted period back to draft. Snapshot stays in place for
-- audit (auditors can compare frozen vs. live after the reopen). The
-- WHERE status = 'submitted' guard makes this a no-op on already-draft
-- periods, returning no rows.
UPDATE b266_periods
SET status = 'draft'
WHERE id = $1 AND status = 'submitted'
RETURNING *;

-- name: B266PeriodCoveringDate :one
-- Returns a submitted period that covers the given date, if any. Mutations
-- whose effective date lands in such a period should be rejected — the
-- snapshot has already been filed with CRA and backdating would create
-- a live-vs-filed discrepancy.
SELECT * FROM b266_periods
WHERE status = 'submitted'
  AND period_start <= $1
  AND period_end   >= $1
LIMIT 1;

-- Aggregation queries for generating B266 sections.

-- name: SumBulkMovementsByReason :many
-- Excludes production_gauge movements whose underlying distillation_run is
-- voided (so voiding a run removes its production LAA from B266). Also
-- excludes regauge_correction movements that reference a void event — those
-- exist purely to balance the ledger and shouldn't show up as their own line.
SELECT bm.reason::text AS reason,
       SUM(bm.laa)::double precision AS total_laa,
       COUNT(*)::int AS count
FROM bulk_movements bm
LEFT JOIN distillation_runs dr
       ON bm.reason = 'production_gauge'
      AND bm.reference_type = 'distillation_run'
      AND dr.id = bm.reference_id
WHERE bm.occurred_at >= $1 AND bm.occurred_at < $2
  AND (dr.id IS NULL OR dr.voided_at IS NULL)
  AND bm.reference_type NOT IN ('distillation_run_void', 'bottling_run_void')
GROUP BY bm.reason
ORDER BY bm.reason;

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
