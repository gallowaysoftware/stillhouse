-- name: GetB266Period :one
SELECT * FROM b266_periods WHERE id = $1;

-- name: GetB266PeriodWithAcknowledger :one
-- The period plus the name of whoever confirmed the figures, for the
-- screens that show the trail rather than act on it.
SELECT p.*, COALESCE(u.display_name, '')::text AS acknowledged_by_name
FROM b266_periods p
LEFT JOIN users u ON u.id = p.filing_acknowledged_by
WHERE p.id = $1;

-- name: GetB266PeriodByDates :one
SELECT * FROM b266_periods
WHERE period_start = $1 AND period_end = $2;

-- name: ListB266Periods :many
SELECT * FROM b266_periods
ORDER BY period_start DESC;

-- name: UpsertB266PeriodDraft :one
-- due_on is set on first generation and left alone afterwards: a change of
-- fiscal-month election must not silently restate when a past return was
-- due. COALESCE keeps whatever the row already had.
INSERT INTO b266_periods (tenant_id, period_start, period_end, status, due_on)
VALUES ($1, $2, $3, 'draft', $4)
ON CONFLICT (tenant_id, period_start, period_end) DO UPDATE
SET updated_at = NOW(),
    due_on     = COALESCE(b266_periods.due_on, EXCLUDED.due_on)
RETURNING *;

-- name: SubmitB266Period :one
-- The acknowledgement is written in the same statement that sets the
-- status, so a submitted period can never exist without one. The table's
-- CHECK holds the other half of that guarantee for any path that is not
-- this one.
UPDATE b266_periods
SET status                 = 'submitted',
    snapshot               = $2,
    submitted_at           = NOW(),
    submitted_by           = $3,
    filing_acknowledged_at = NOW(),
    filing_acknowledged_by = $3,
    filing_acknowledgement = $4
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

-- name: SumBottlingDutyInPeriod :one
-- Duty that crystallised at packaging during the period, split by the two
-- rate bands, because they are not charged in the same unit: above 7% ABV
-- per litre of absolute alcohol, at or below 7% per litre of product. The
-- mirror of SumRemovalsInPeriod, and it exists for the same reason — a
-- return that quotes one blended rate against a mixed total fails its own
-- arithmetic.
--
-- Only runs carrying a duty amount count. A run with NULL duty is not a
-- duty event: either the tenant pays at removal, or the run predates the
-- tenant's duty-point cutover and its stock is dutied on its way out
-- instead.
SELECT COALESCE(SUM(br.duty_amount_cad), 0)::double precision AS total_duty,
       COALESCE(SUM(br.bottle_count) FILTER (WHERE p.target_abv_pct > 7), 0)::int
           AS over7_bottles,
       COALESCE(SUM(br.bottle_count * p.bottle_size_ml / 1000.0 * p.target_abv_pct / 100.0)
                FILTER (WHERE p.target_abv_pct > 7), 0)::double precision
           AS over7_laa,
       COALESCE(SUM(br.duty_amount_cad) FILTER (WHERE p.target_abv_pct > 7), 0)::double precision
           AS over7_duty,
       COALESCE(SUM(br.bottle_count) FILTER (WHERE p.target_abv_pct <= 7), 0)::int
           AS under7_bottles,
       COALESCE(SUM(br.bottle_count * p.bottle_size_ml / 1000.0)
                FILTER (WHERE p.target_abv_pct <= 7), 0)::double precision
           AS under7_litres,
       COALESCE(SUM(br.duty_amount_cad) FILTER (WHERE p.target_abv_pct <= 7), 0)::double precision
           AS under7_duty,
       -- Bottles and LAA packaged as duty-paid: everything with a duty
       -- amount on it. The complement — packaged non-duty-paid — is the
       -- rest of the period's bottling, and the projection takes the
       -- difference rather than running a second scan.
       COALESCE(SUM(br.bottle_count), 0)::int AS duty_paid_bottles,
       COALESCE(SUM(br.bottle_count * p.bottle_size_ml / 1000.0 * p.target_abv_pct / 100.0),
                0)::double precision AS duty_paid_laa
FROM bottling_runs br
JOIN products p ON p.id = br.product_id
WHERE br.bottling_date >= $1 AND br.bottling_date < $2
  AND br.voided_at IS NULL
  AND br.duty_amount_cad IS NOT NULL;

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

-- name: SumBulkOnHandAsOf :one
-- Bulk LAA on hand as at a moment, not right now.
--
-- This used to sum current_laa with no date at all, and its own comment
-- conceded that was "fine if generated promptly after period close".
-- Generating May's return in August therefore reported August's balance as
-- May's closing figure — and because the opening balance is reverse-walked
-- from the closing one, both ends moved together and the arithmetic still
-- tied out. A return that is internally consistent and factually wrong is
-- the worst shape for an error, because nothing looks off.
--
-- The walk goes backwards from the running total rather than forwards from
-- zero, deliberately: when as_of is now, the "after" set is empty and this
-- returns exactly what the old query did, so a promptly-generated return
-- does not move. Only movements between the period end and now are
-- subtracted, so nothing depends on the ledger being complete back to the
-- distillery's first day.
--
-- Net effect on total on-hand: a movement into a container adds, a movement
-- out subtracts, and one with both ends set — a barrel fill, a blend — is
-- internal and nets to zero. Void corrections carry their own offsetting
-- row (see VoidDistillationRun), so including everything is what makes the
-- walk reconcile.
--
-- Known edge: a container archived after as_of is excluded from the running
-- total but held alcohol at as_of. Archiving requires an empty container,
-- so the LAA involved is zero.
--
-- Possession, not ownership. EDM10-1-7 page 3 asks for all bulk spirits in
-- our possession whatever anyone owns, so a customer's cask maturing in
-- our rackhouse is here and a parcel of our own whisky at a partner's
-- bonded warehouse is not.
--
-- The filter is on the running total only, and the movement side is left
-- alone deliberately. Filtering both would be the obvious thing and would
-- be wrong: the walk needs the movements a departing container made while
-- it was still ours to report. It works out because a possession change
-- writes a movement for the whole balance (rpc.SetBulkPossession), and
-- nothing else may be recorded against a container held elsewhere:
--
--   left after as_of     excluded from running; its exit movement is
--                        subtracted, adding the balance back — which is
--                        what was on hand at as_of.
--   returned after as_of included in running; its return movement is
--                        subtracted, taking it back out.
--   never here          cannot happen: a container is created and adopted
--                        in our possession and reaches held_elsewhere only
--                        through the transition above, so there is always
--                        a movement to reconcile against.
WITH running AS (
    SELECT COALESCE(SUM(current_laa), 0)::double precision AS total_laa
    FROM bulk_containers
    WHERE NOT archived
      AND possession = 'held'
), moved_after AS (
    SELECT COALESCE(SUM(
        CASE WHEN destination_container_id IS NOT NULL THEN laa ELSE 0 END
      - CASE WHEN source_container_id      IS NOT NULL THEN laa ELSE 0 END
    ), 0)::double precision AS net_laa
    FROM bulk_movements
    WHERE occurred_at >= sqlc.arg(as_of)
)
SELECT (running.total_laa - moved_after.net_laa)::double precision AS total_laa
FROM running, moved_after;

-- name: SumPackagedOnHandAsOf :one
-- Packaged LAA and bottles on hand as at a moment. Same reverse walk as
-- SumBulkOnHandAsOf and for the same reason: packaged inventory only ever
-- receives bottling runs and only ever loses removals, so undoing both back
-- to the period end recovers the balance that was actually on hand then.
--
-- LAA is the same approximation the packaged section has always used:
-- bottles × bottle_size × target_abv. Removals carry their own total_laa,
-- computed the same way at the time, so the two sides subtract cleanly.
--
-- Known edge: a run or removal dated inside the period but voided after it
-- is treated as never having happened, which is how the reason sums above
-- already treat voids. The period lock stops that arising for a period
-- already filed.
WITH running AS (
    SELECT COALESCE(SUM(pi.bottles_on_hand * p.bottle_size_ml * p.target_abv_pct / 100000.0), 0)::double precision AS total_laa,
           COALESCE(SUM(pi.bottles_on_hand), 0)::int AS total_bottles
    FROM packaged_inventory pi
    JOIN products p ON p.id = pi.product_id
), packaged_after AS (
    SELECT COALESCE(SUM(br.bottle_count * p.bottle_size_ml / 1000.0 * p.target_abv_pct / 100.0), 0)::double precision AS laa,
           COALESCE(SUM(br.bottle_count), 0)::int AS bottles
    FROM bottling_runs br
    JOIN products p ON p.id = br.product_id
    WHERE br.bottling_date >= sqlc.arg(as_of) AND br.voided_at IS NULL
), removed_after AS (
    SELECT COALESCE(SUM(total_laa), 0)::double precision AS laa,
           COALESCE(SUM(bottles_removed), 0)::int AS bottles
    FROM packaging_removals
    WHERE removal_date >= sqlc.arg(as_of) AND voided_at IS NULL
)
SELECT (running.total_laa     - packaged_after.laa     + removed_after.laa)::double precision AS total_laa,
       (running.total_bottles - packaged_after.bottles + removed_after.bottles)::int          AS total_bottles
FROM running, packaged_after, removed_after;

-- name: BulkOwnershipSplitAsOf :one
-- What the closing balance is made of. Not a line on the form: EDM10-1-7
-- page 3 asks for everything in our possession and nothing else. It is
-- here because a licensee signing a return that includes a customer's
-- casks should be able to see that it does, and one whose own casks are
-- at a partner's warehouse should be able to see why they are absent.
--
-- Deliberately read as at now rather than walked back to the period end.
-- Ownership and possession are current facts with no ledger behind them
-- yet, so a walk would be a fiction; the figures are labelled as current
-- wherever they are shown. Getting this wrong in the other direction —
-- presenting a walked figure that was never computed — is how a return
-- becomes internally consistent and factually wrong.
SELECT
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held'), 0)::double precision
        AS held_for_others_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS held_elsewhere_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS third_party_elsewhere_laa
FROM bulk_containers
WHERE NOT archived;
