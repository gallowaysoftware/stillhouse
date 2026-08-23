-- name: CreateLabResult :one
INSERT INTO lab_results (
    tenant_id, container_id, production_gauge_id, bottling_run_id, mash_run_id,
    analyte, value, uom, spec_min, spec_max, status,
    method, laboratory, reference, sampled_on, reported_on, notes, recorded_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
) RETURNING *;

-- name: ListLabResults :many
-- Filtered by whichever subject the caller names; all of them NULL
-- returns the tenant's whole lab history, newest first.
SELECT r.*, COALESCE(u.display_name, '') AS recorded_by_name
FROM lab_results r
LEFT JOIN users u ON u.id = r.recorded_by
WHERE (sqlc.narg(container_id)::UUID IS NULL OR r.container_id = sqlc.narg(container_id)::UUID)
  AND (sqlc.narg(bottling_run_id)::UUID IS NULL OR r.bottling_run_id = sqlc.narg(bottling_run_id)::UUID)
  AND (sqlc.narg(production_gauge_id)::UUID IS NULL OR r.production_gauge_id = sqlc.narg(production_gauge_id)::UUID)
  AND (sqlc.narg(mash_run_id)::UUID IS NULL OR r.mash_run_id = sqlc.narg(mash_run_id)::UUID)
ORDER BY r.reported_on DESC, r.created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: CountFailedLabResultsForRun :one
-- Failing results attached to a bottling run, or to the container it
-- drew from. A release decision should not have to be told about a
-- failure recorded against the tank the lot came out of.
SELECT COUNT(*)::INTEGER FROM lab_results r
WHERE r.status = 'fail'
  AND (r.bottling_run_id = sqlc.arg(bottling_run_id)::UUID
       OR r.container_id = sqlc.narg(container_id)::UUID);

-- name: ReleasePackagedLot :one
-- Releasing clears any hold: a lot that has been looked at again and
-- passed is released, not simultaneously held.
UPDATE packaged_inventory
SET released_at   = NOW(),
    released_by   = $2,
    release_notes = $3,
    held_at       = NULL,
    held_by       = NULL,
    hold_reason   = ''
WHERE id = $1
RETURNING *;

-- name: HoldPackagedLot :one
-- Holding does NOT clear the release. A lot held after release is a
-- recall in its early form, and erasing the fact that somebody released
-- it would remove the most important part of that record.
UPDATE packaged_inventory
SET held_at     = NOW(),
    held_by     = $2,
    hold_reason = $3
WHERE id = $1
RETURNING *;

-- name: GetPackagedLotReleaseState :one
SELECT id, released_at, held_at, hold_reason, bottling_run_id
FROM packaged_inventory WHERE id = $1;
