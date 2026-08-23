-- name: CreateRedistillation :one
INSERT INTO redistillations (
    tenant_id, source_container_id, bulk_movement_id, reason, taken_on,
    volume_taken_l, abv_taken_pct, laa_taken, notes, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: RecordRedistillationOutput :one
-- Closes the loop. laa_produced and produced_on are set together — the
-- CHECK enforces it — so a run can never be half-recorded, and loss_laa
-- becomes computable at exactly the moment both halves are known.
UPDATE redistillations
SET distillation_run_id = $2,
    laa_produced        = $3,
    produced_on         = $4
WHERE id = $1 AND laa_produced IS NULL
RETURNING *;

-- name: MarkRedistillationLossClassified :one
UPDATE redistillations SET loss_classified_at = NOW() WHERE id = $1 RETURNING *;

-- name: GetRedistillation :one
SELECT * FROM redistillations WHERE id = $1;

-- name: ListRedistillations :many
SELECT r.*,
       bc.name AS source_container_name,
       COALESCE(dr.run_no, 0)::INTEGER AS distillation_run_no,
       COALESCE(u.display_name, '') AS recorded_by_name
FROM redistillations r
JOIN bulk_containers bc ON bc.id = r.source_container_id
LEFT JOIN distillation_runs dr ON dr.id = r.distillation_run_id
LEFT JOIN users u ON u.id = r.recorded_by
WHERE (sqlc.arg(open_only)::BOOLEAN = FALSE OR r.laa_produced IS NULL)
ORDER BY r.taken_on DESC, r.created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: AlertOpenRedistillations :many
-- Spirit that left stock into the still and has no output recorded after
-- long enough that it should have. Alcohol off the books is the one
-- shape of gap a period-end reconciliation cannot explain.
SELECT r.id, r.taken_on, r.laa_taken, bc.name AS source_container_name
FROM redistillations r
JOIN bulk_containers bc ON bc.id = r.source_container_id
WHERE r.laa_produced IS NULL
  AND r.taken_on < sqlc.arg(cutoff)::DATE;

-- name: RedistillationPeriodSummary :one
-- What went back through the still in a period, and what it cost. The
-- figures EDM3-1-1 para 41 asks to be kept.
SELECT
    COUNT(*)::INTEGER                                        AS event_count,
    COALESCE(SUM(laa_taken), 0)::DOUBLE PRECISION            AS laa_taken,
    COALESCE(SUM(laa_produced), 0)::DOUBLE PRECISION         AS laa_produced,
    COALESCE(SUM(loss_laa), 0)::DOUBLE PRECISION             AS loss_laa,
    COUNT(*) FILTER (WHERE laa_produced IS NULL)::INTEGER    AS still_open
FROM redistillations
WHERE taken_on >= sqlc.arg(period_start)::DATE
  AND taken_on <= sqlc.arg(period_end)::DATE;
