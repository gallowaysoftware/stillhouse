-- Losses and their duty treatment.
--
-- "A loss" is any movement whose reason takes alcohol out of the ledger
-- without it going anywhere: evaporation, spirits that cannot be accounted
-- for, and destructions. The list is repeated in each query rather than
-- factored into a view so that adding a reason to it is a deliberate edit
-- somebody reads, not a silent inheritance.

-- name: ListLosses :many
SELECT bm.*,
       COALESCE(src.name, dst.name, '')::text AS container_name,
       COALESCE(u.display_name, '')::text     AS classified_by_name
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN bulk_containers dst ON dst.id = bm.destination_container_id
LEFT JOIN users u             ON u.id  = bm.loss_classified_by
WHERE bm.reason IN ('loss_evaporation', 'loss_unaccounted', 'destruction')
  AND bm.reference_type NOT IN ('distillation_run_void', 'bottling_run_void')
  AND (NOT sqlc.arg(unclassified_only)::boolean OR bm.loss_duty_treatment = 'unclassified')
  AND (sqlc.narg('period_start')::date IS NULL OR bm.occurred_at >= sqlc.narg('period_start')::date)
  AND (sqlc.narg('period_end')::date   IS NULL OR bm.occurred_at <  sqlc.narg('period_end')::date + 1)
ORDER BY bm.occurred_at DESC
LIMIT 500;

-- name: ClassifyLoss :one
-- Only a loss can be classified. The reason guard is here rather than only
-- in Go so that a caller cannot quietly attach a duty treatment to a
-- production gauge and have it counted.
UPDATE bulk_movements
SET loss_duty_treatment      = $2,
    loss_treatment_authority = $3,
    loss_classified_by       = $4,
    loss_classified_at       = NOW()
WHERE id = $1
  AND reason IN ('loss_evaporation', 'loss_unaccounted', 'destruction')
RETURNING *;

-- name: SumLossesByTreatmentInPeriod :one
-- The three figures that must sum to bulk_losses_laa. Destructions are
-- reported on their own line, so they are excluded here — a destruction is
-- not part of the losses total, and counting it in both places would
-- overstate what left the warehouse.
SELECT COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'relieved'), 0)::double precision
           AS relieved_laa,
       COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'dutiable'), 0)::double precision
           AS dutiable_laa,
       COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'unclassified'), 0)::double precision
           AS unclassified_laa,
       COUNT(*) FILTER (WHERE loss_duty_treatment = 'unclassified')::int
           AS unclassified_count
FROM bulk_movements
WHERE reason IN ('loss_evaporation', 'loss_unaccounted')
  AND reference_type NOT IN ('distillation_run_void', 'bottling_run_void')
  AND occurred_at >= $1 AND occurred_at < $2;

-- name: SumDestructionsByTreatmentInPeriod :one
-- Destructions carry a treatment too — an unapproved destruction is not
-- relieved — but they are reported on their own line, so they are summed
-- separately from the losses total.
SELECT COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'relieved'), 0)::double precision
           AS relieved_laa,
       COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'dutiable'), 0)::double precision
           AS dutiable_laa,
       COALESCE(SUM(laa) FILTER (WHERE loss_duty_treatment = 'unclassified'), 0)::double precision
           AS unclassified_laa,
       COUNT(*) FILTER (WHERE loss_duty_treatment = 'unclassified')::int
           AS unclassified_count
FROM bulk_movements
WHERE reason = 'destruction'
  AND occurred_at >= $1 AND occurred_at < $2;

-- name: ListDutiableLossesInPeriod :many
-- Each dutiable loss with the day it happened, so it can be charged at the
-- rate in force then rather than at the period's. A semi-annual period
-- always spans an indexation, so a period rate would charge half of them
-- at the wrong one.
SELECT occurred_at, laa
FROM bulk_movements
WHERE reason IN ('loss_evaporation', 'loss_unaccounted', 'destruction')
  AND reference_type NOT IN ('distillation_run_void', 'bottling_run_void')
  AND loss_duty_treatment = 'dutiable'
  AND occurred_at >= $1 AND occurred_at < $2;
