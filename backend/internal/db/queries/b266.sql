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
-- Three different quantities, and conflating them puts a negative opening
-- balance on the return:
--   drawn_laa    — what left bulk (the tank gauge)
--   packaged_laa — what became sealed bottles
--   loss_laa     — the difference, spilled or left in the filler
-- Packaged inventory only ever received packaged_laa, so that is the line
-- that closes against it; the bulk side's transfer-to-packaging figure is
-- drawn_laa, and the loss is what reconciles the two.
SELECT COALESCE(SUM(br.tank_gauge_laa), 0)::double precision AS total_laa,
       COALESCE(SUM(br.bottle_count * p.bottle_size_ml / 1000.0 * p.target_abv_pct / 100.0),
                0)::double precision AS packaged_laa,
       COALESCE(SUM(br.bottling_loss_l * p.target_abv_pct / 100.0),
                0)::double precision AS loss_laa,
       COUNT(*)::int AS run_count,
       COALESCE(SUM(br.bottle_count), 0)::int AS total_bottles
FROM bottling_runs br
JOIN products p ON p.id = br.product_id
WHERE br.bottling_date >= $1 AND br.bottling_date < $2
  AND br.voided_at IS NULL;

-- name: SumRemovalsInPeriod :one
-- Split by rate band, because the two bands are not taxed in the same
-- unit: spirits above 7% ABV pay per litre of absolute alcohol, at or
-- below 7% pay per litre of product. Reporting one blended "rate per LAA"
-- against a total LAA made the return fail its own arithmetic as soon as a
-- period contained both — 7.775 LAA at a stated $14.117 is $109.76, while
-- the duty actually owed was $97.41. The B266 has separate lines for the
-- two bands for exactly this reason.
SELECT COALESCE(SUM(total_laa), 0)::double precision       AS total_laa,
       COALESCE(SUM(duty_amount_cad), 0)::double precision AS total_duty,
       COALESCE(SUM(bottles_removed), 0)::int              AS total_bottles,
       COUNT(*)::int                                       AS removal_count,
       COALESCE(SUM(total_laa) FILTER (WHERE bottle_abv_pct > 7), 0)::double precision
           AS over7_laa,
       COALESCE(SUM(duty_amount_cad) FILTER (WHERE bottle_abv_pct > 7), 0)::double precision
           AS over7_duty,
       COALESCE(SUM(bottles_removed) FILTER (WHERE bottle_abv_pct > 7), 0)::int
           AS over7_bottles,
       COALESCE(SUM(total_litres) FILTER (WHERE bottle_abv_pct <= 7), 0)::double precision
           AS under7_litres,
       COALESCE(SUM(duty_amount_cad) FILTER (WHERE bottle_abv_pct <= 7), 0)::double precision
           AS under7_duty,
       COALESCE(SUM(bottles_removed) FILTER (WHERE bottle_abv_pct <= 7), 0)::int
           AS under7_bottles
FROM packaging_removals
WHERE removal_date >= $1 AND removal_date < $2
  AND voided_at IS NULL;

-- name: B266PeriodsOverlapping :many
-- Any period that shares a day with the given range and is not that exact
-- range. Two returns covering the same day would report the same alcohol
-- twice.
--
-- Named parameters deliberately: with positional $1/$2, sqlc names the
-- struct fields after the column each is compared against, so `period_end
-- >= $1` made $1 "PeriodEnd" and the caller's PeriodStart silently landed
-- in the wrong slot.
SELECT * FROM b266_periods
WHERE period_start <= sqlc.arg(range_end)
  AND period_end   >= sqlc.arg(range_start)
  AND NOT (period_start = sqlc.arg(range_start) AND period_end = sqlc.arg(range_end))
ORDER BY period_start;

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
