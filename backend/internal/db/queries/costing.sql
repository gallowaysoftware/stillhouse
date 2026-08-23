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

-- name: ProductionGaugeWIPCost :many
-- Spirit gauged into bulk, valued by walking forward from the mashes
-- behind it. PLAN E7.
--
-- Four steps, three of which have a recorded basis and need no convention:
--
--   mash_cost      what the mash's materials cost, at the lot each came
--                  from. priced is false when any usage has no lot or the
--                  lot has no cost — the mash is then unvalued rather than
--                  cheap, and the difference is reported.
--   ferment_share  a mash that fed one fermentation gives it everything.
--                  One that fed several splits by initial_volume_l, and
--                  refuses (NULL) if any of them is missing it, because a
--                  share computed over the ones that happen to have a
--                  volume would silently overstate them.
--   charge_share   a fermentation's share of each still it was charged
--                  to, on the licensee's stated basis: litres charged or
--                  LAA charged. The denominator is every charge that
--                  fermentation made, not only the ones inside the
--                  reporting period, because the share is a property of
--                  the fermentation.
--   gauge          production_gauges is UNIQUE on distillation_run_id, so
--                  a run's cost is its gauge's cost entire.
--
-- A fermentation whose wash was never fully charged leaves cost behind on
-- purpose. Wash that never reached a still is not work in progress; it is
-- a loss, and allocating it to the spirit that did get made would inflate
-- the value of that spirit. unallocated_cad reports what stayed behind so
-- it is visible rather than merely absent.
WITH mash_cost AS (
    SELECT mu.mash_run_id,
           COALESCE(SUM(mu.quantity_used * COALESCE(ml.landed_unit_cost_cad, ml.unit_cost_cad)), 0)::double precision AS cost_cad,
           BOOL_AND(ml.id IS NOT NULL AND COALESCE(ml.landed_unit_cost_cad, ml.unit_cost_cad) IS NOT NULL) AS priced,
           COUNT(*) FILTER (WHERE ml.id IS NULL OR COALESCE(ml.landed_unit_cost_cad, ml.unit_cost_cad) IS NULL)::int AS unpriced_lines
    FROM mash_ingredient_usage mu
    LEFT JOIN material_lots ml ON ml.id = mu.material_lot_id
    GROUP BY mu.mash_run_id
), ferment_share AS (
    SELECT f.id AS fermentation_run_id,
           f.mash_run_id,
           CASE
             WHEN COUNT(*) OVER (PARTITION BY f.mash_run_id) = 1 THEN 1.0
             WHEN BOOL_AND(f.initial_volume_l IS NOT NULL) OVER (PARTITION BY f.mash_run_id)
                  AND SUM(f.initial_volume_l) OVER (PARTITION BY f.mash_run_id) > 0
               THEN f.initial_volume_l / SUM(f.initial_volume_l) OVER (PARTITION BY f.mash_run_id)
             ELSE NULL
           END::double precision AS share
    FROM fermentation_runs f
), charge_basis AS (
    SELECT dc.distillation_run_id,
           dc.fermentation_run_id,
           CASE WHEN sqlc.arg(basis)::text = 'charged_laa'
                THEN dc.volume_charged_l * dc.abv_pct / 100
                ELSE dc.volume_charged_l
           END::double precision AS amount
    FROM distillation_charges dc
), charge_share AS (
    SELECT cb.distillation_run_id,
           cb.fermentation_run_id,
           CASE WHEN SUM(cb.amount) OVER (PARTITION BY cb.fermentation_run_id) > 0
                THEN cb.amount / SUM(cb.amount) OVER (PARTITION BY cb.fermentation_run_id)
                ELSE NULL
           END::double precision AS share
    FROM charge_basis cb
)
SELECT g.id,
       g.gauge_date,
       g.laa,
       g.distillation_run_id,
       COALESCE(bc.name, '')::text AS container_name,
       -- The value carried into WIP by this gauge. NULL where any step of
       -- the walk refused, which is why the flags below travel with it.
       -- The sum is NULL when any step of the walk refused. That has to
       -- stay distinguishable from a genuine zero all the way to the
       -- caller — a cost of nothing and a cost nobody could compute are
       -- different claims, and conflating them produces a figure that
       -- reconciles and never gets looked at again. Carried as an
       -- explicit flag rather than as a nullable float, because the flag
       -- is what the caller has to branch on and a NULL that reads as 0.0
       -- one layer up is exactly the failure being guarded against.
       COALESCE(SUM(mc.cost_cad * fs.share * cs.share), 0)::double precision AS cost_cad,
       (SUM(mc.cost_cad * fs.share * cs.share) IS NOT NULL)::boolean AS cost_known,
       -- Every reason this gauge could not be valued, so the operator is
       -- told which mash or which fermentation to go and fix.
       BOOL_AND(COALESCE(mc.priced, false))::boolean            AS all_mashes_priced,
       BOOL_AND(fs.share IS NOT NULL)::boolean                  AS all_ferment_shares_known,
       BOOL_AND(cs.share IS NOT NULL)::boolean                  AS all_charge_shares_known,
       COUNT(*)::int                                            AS charge_count,
       COALESCE(SUM(mc.unpriced_lines), 0)::int                 AS unpriced_material_lines
FROM production_gauges g
JOIN distillation_runs dr    ON dr.id = g.distillation_run_id
JOIN bulk_containers bc      ON bc.id = g.destination_container_id
JOIN charge_share cs         ON cs.distillation_run_id = g.distillation_run_id
JOIN ferment_share fs        ON fs.fermentation_run_id = cs.fermentation_run_id
LEFT JOIN mash_cost mc       ON mc.mash_run_id = fs.mash_run_id
WHERE g.gauge_date >= sqlc.arg(period_start)::timestamptz
  AND g.gauge_date <  sqlc.arg(period_end)::timestamptz
  -- A voided run's spirit went back out of the ledger; carrying its cost
  -- into WIP would value alcohol that is not there. Voids are how
  -- Stillhouse reverses a distillation, so this is the ordinary case
  -- rather than an edge one.
  AND dr.voided_at IS NULL
GROUP BY g.id, g.gauge_date, g.laa, g.distillation_run_id, bc.name
ORDER BY g.gauge_date, g.id;

-- name: GetWIPChargeBasis :one
SELECT wip_charge_basis FROM tenants WHERE id = $1;

-- name: SetWIPChargeBasis :one
-- Stated by the licensee, never defaulted. See 000061.
UPDATE tenants SET wip_charge_basis = $2, updated_at = NOW()
WHERE id = $1
RETURNING wip_charge_basis;
