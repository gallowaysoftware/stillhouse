-- name: SaveCostRates :one
INSERT INTO cost_rates (
    tenant_id, effective_from, labour_rate_cad_per_hour,
    overhead_basis, overhead_rate, notes, created_by
) VALUES ($1, $2, $3, sqlc.narg(overhead_basis)::overhead_basis, $4, $5, $6)
ON CONFLICT (tenant_id, effective_from) DO UPDATE
SET labour_rate_cad_per_hour = EXCLUDED.labour_rate_cad_per_hour,
    overhead_basis           = EXCLUDED.overhead_basis,
    overhead_rate            = EXCLUDED.overhead_rate,
    notes                    = EXCLUDED.notes
RETURNING *;

-- name: DeleteCostRates :exec
DELETE FROM cost_rates WHERE id = $1;

-- name: ListCostRates :many
SELECT * FROM cost_rates ORDER BY effective_from DESC;

-- name: CostRatesInForceOn :one
-- The rates that applied on a given day. Effective-dated so that costing
-- a batch from March does not use April's rate — a rate change must not
-- restate a period an accountant has already taken into a set of books.
SELECT * FROM cost_rates
WHERE effective_from <= sqlc.arg(on_date)::date
ORDER BY effective_from DESC
LIMIT 1;

-- name: RecordLabour :one
INSERT INTO labour_entries (
    tenant_id, mash_run_id, distillation_run_id, bottling_run_id, work_order_id,
    worked_on, hours, worked_by, worked_by_name, rate_cad_per_hour, notes, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: DeleteLabourEntry :exec
DELETE FROM labour_entries WHERE id = $1;

-- name: ListLabourForSubject :many
SELECT le.*, COALESCE(u.display_name, '') AS worked_by_display
FROM labour_entries le
LEFT JOIN users u ON u.id = le.worked_by
WHERE (sqlc.narg(mash_run_id)::uuid         IS NULL OR le.mash_run_id         = sqlc.narg(mash_run_id)::uuid)
  AND (sqlc.narg(distillation_run_id)::uuid IS NULL OR le.distillation_run_id = sqlc.narg(distillation_run_id)::uuid)
  AND (sqlc.narg(bottling_run_id)::uuid     IS NULL OR le.bottling_run_id     = sqlc.narg(bottling_run_id)::uuid)
  AND (sqlc.narg(work_order_id)::uuid       IS NULL OR le.work_order_id       = sqlc.narg(work_order_id)::uuid)
  AND (sqlc.narg(mash_run_id)::uuid IS NOT NULL
    OR sqlc.narg(distillation_run_id)::uuid IS NOT NULL
    OR sqlc.narg(bottling_run_id)::uuid IS NOT NULL
    OR sqlc.narg(work_order_id)::uuid IS NOT NULL)
ORDER BY le.worked_on, le.created_at;

-- name: SumLabourForBottlingRun :one
-- Hours booked directly to the bottling run. Deliberately not the whole
-- chain: hours on the mash and the distillation behind it are counted
-- separately, because they are shared across everything that chain fed
-- and a run that took a tenth of a tank should not carry all of it.
SELECT COALESCE(SUM(hours), 0)::double precision AS hours,
       COUNT(*)::int AS entries
FROM labour_entries WHERE bottling_run_id = $1;

-- name: SumLabourForMashRuns :one
SELECT COALESCE(SUM(hours), 0)::double precision AS hours
FROM labour_entries WHERE mash_run_id = ANY(sqlc.arg(ids)::uuid[]);

-- name: SumLabourForDistillationRuns :one
SELECT COALESCE(SUM(hours), 0)::double precision AS hours
FROM labour_entries WHERE distillation_run_id = ANY(sqlc.arg(ids)::uuid[]);

-- name: LabourHoursInPeriod :one
SELECT COALESCE(SUM(hours), 0)::double precision AS hours,
       COUNT(*)::int AS entries,
       COUNT(*) FILTER (WHERE rate_cad_per_hour IS NOT NULL)::int AS overridden
FROM labour_entries
WHERE worked_on >= sqlc.arg(period_start)::date
  AND worked_on <= sqlc.arg(period_end)::date;

-- name: ValueBulkForWIP :many
-- Everything still in bulk, which is what work in progress means for a
-- distillery: alcohol made and not yet packaged. Barrels included — a
-- cask maturing is the largest WIP a whisky distillery has.
--
-- Owned stock only. Spirits held for a customer are on the B266 and not
-- on the balance sheet (stage 176).
SELECT bc.id, bc.name, bc.kind, bc.current_volume_l, bc.current_abv_pct,
       bc.current_laa, bc.possession
FROM bulk_containers bc
WHERE NOT bc.archived
  AND bc.owner_customer_id IS NULL
  AND bc.current_laa > 0
ORDER BY bc.kind, bc.name;

-- name: ValuePackagedForFinishedGoods :many
-- Bottles on hand, with the run each came from so the cost that run
-- carried can be applied. A lot with no run behind it (adopted stock)
-- comes back with a NULL run and is reported as unvalued rather than
-- valued at zero.
SELECT pi.id, pi.lot_code, pi.jurisdiction, pi.bottles_on_hand,
       pi.bottling_run_id, p.name AS product_name,
       p.bottle_size_ml, p.target_abv_pct
FROM packaged_inventory pi
JOIN products p ON p.id = pi.product_id
WHERE pi.bottles_on_hand > 0
  -- Owned stock only. A customer's bottles sitting in the warehouse are
  -- on the B266 and not on the balance sheet — the same rule the bulk
  -- side follows (stage 176), now that a lot knows whose it is.
  AND pi.owner_customer_id IS NULL
ORDER BY p.name, pi.lot_code;

-- name: BottlingRunsInPeriodForWIP :many
-- The runs that moved alcohol out of work in progress and into finished
-- goods during a period. Voided runs are excluded — their effect is
-- reversed elsewhere and posting both sides would double the entry.
SELECT br.id, br.bottling_date, br.bottle_count, br.tank_gauge_laa,
       p.name AS product_name, br.lot_code
FROM bottling_runs br
JOIN products p ON p.id = br.product_id
WHERE br.bottling_date >= sqlc.arg(period_start)::date
  AND br.bottling_date <= sqlc.arg(period_end)::date
  AND br.voided_at IS NULL
ORDER BY br.bottling_date, br.lot_code;

-- name: CountThirdPartyPackagedLots :one
SELECT COUNT(*)::INTEGER AS n
FROM packaged_inventory
WHERE owner_customer_id IS NOT NULL AND bottles_on_hand > 0;
