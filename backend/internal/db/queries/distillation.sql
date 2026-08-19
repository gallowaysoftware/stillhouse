-- name: NextDistillationRunNo :one
SELECT COALESCE(MAX(run_no), 0)::int + 1 AS next FROM distillation_runs;

-- name: CreateDistillationRun :one
INSERT INTO distillation_runs (
    tenant_id, run_no, still_label, run_date, status, notes
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetDistillationRun :one
SELECT * FROM distillation_runs WHERE id = $1;

-- name: ListDistillationRuns :many
SELECT * FROM distillation_runs
WHERE (sqlc.narg('status')::distillation_status IS NULL OR status = sqlc.narg('status')::distillation_status)
ORDER BY run_date DESC, run_no DESC;

-- name: UpdateDistillationStatus :one
UPDATE distillation_runs SET status = $2 WHERE id = $1 RETURNING *;

-- name: AddDistillationCharge :one
INSERT INTO distillation_charges (
    tenant_id, distillation_run_id, fermentation_run_id,
    volume_charged_l, abv_pct, charge_order, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListDistillationCharges :many
SELECT dc.*,
       fr.fermenter_label AS fermenter_label,
       mr.mash_no         AS mash_no
FROM distillation_charges dc
JOIN fermentation_runs fr ON fr.id = dc.fermentation_run_id
JOIN mash_runs         mr ON mr.id = fr.mash_run_id
WHERE dc.distillation_run_id = $1
ORDER BY dc.charge_order;

-- name: AddDistillationCut :one
INSERT INTO distillation_cuts (
    tenant_id, distillation_run_id, kind, volume_l, abv_pct, cut_order, observed_at, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: ListDistillationCuts :many
SELECT * FROM distillation_cuts
WHERE distillation_run_id = $1
ORDER BY cut_order, observed_at;

-- name: GetDistillationCut :one
SELECT * FROM distillation_cuts WHERE id = $1;

-- name: UpdateDistillationCut :one
UPDATE distillation_cuts
SET kind        = $2,
    volume_l    = $3,
    abv_pct     = $4,
    cut_order   = $5,
    observed_at = $6,
    notes       = $7
WHERE id = $1
RETURNING *;

-- name: DeleteDistillationCut :exec
DELETE FROM distillation_cuts WHERE id = $1;

-- name: CreateProductionGauge :one
-- volume_l / abv_pct are the values AT 20 °C; observed_* preserve what the
-- operator read off the instrument. See migration 000023.
INSERT INTO production_gauges (
    tenant_id, distillation_run_id, destination_container_id, bulk_movement_id,
    gauge_date, volume_l, abv_pct, temperature_c, gauger_user_id, notes,
    observed_volume_l, observed_density_kg_m3, volume_factor_c, strength_source
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
) RETURNING *;

-- name: GetProductionGaugeByRun :one
SELECT * FROM production_gauges WHERE distillation_run_id = $1;

-- name: VoidDistillationRun :one
UPDATE distillation_runs
SET voided_at = NOW(),
    voided_by = $2,
    voided_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;
