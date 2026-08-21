-- name: CreateInstrument :one
INSERT INTO instruments (
    tenant_id, kind, label, manufacturer, model, serial_no,
    approval_reference, approval_date, approval_expires_on,
    calibration_interval_days, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: UpdateInstrument :one
-- Deliberately cannot change kind or serial_no. Both are the instrument's
-- identity — the serial is what CRA approved — and editing either would
-- silently re-point every determination already made with it at a
-- different physical device.
UPDATE instruments
SET label                     = $2,
    manufacturer              = $3,
    model                     = $4,
    approval_reference        = $5,
    approval_date             = $6,
    approval_expires_on       = $7,
    calibration_interval_days = $8,
    notes                     = $9
WHERE id = $1
RETURNING *;

-- name: SetInstrumentStatus :one
UPDATE instruments
SET status = $2, status_reason = $3
WHERE id = $1
RETURNING *;

-- name: GetInstrument :one
SELECT * FROM instruments WHERE id = $1;

-- name: ListInstruments :many
-- Retired instruments are excluded unless asked for: the register an
-- operator picks from at the bench should hold what is in service. They
-- are never deleted, so a determination made years ago still resolves.
SELECT * FROM instruments
WHERE (sqlc.arg(include_retired)::boolean OR status <> 'retired')
  AND (sqlc.narg(kind)::instrument_kind IS NULL OR kind = sqlc.narg(kind)::instrument_kind)
ORDER BY kind, label;

-- name: CreateCalibration :one
INSERT INTO instrument_calibrations (
    tenant_id, instrument_id, calibrated_on, performed_by,
    certificate_ref, passed, notes, recorded_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: ListCalibrations :many
SELECT * FROM instrument_calibrations
WHERE instrument_id = $1
ORDER BY calibrated_on DESC, created_at DESC;

-- name: LatestCalibration :one
-- The most recent PASSED calibration. A failed check is history worth
-- keeping, but it is not the date the next one is counted from — an
-- instrument that failed its check has not been calibrated.
SELECT * FROM instrument_calibrations
WHERE instrument_id = $1 AND passed
ORDER BY calibrated_on DESC, created_at DESC
LIMIT 1;

-- name: LatestCalibrationsForInstruments :many
-- One row per instrument: its most recent passed calibration. DISTINCT ON
-- rather than a correlated subquery so listing the register is one query
-- regardless of how many instruments a distillery holds.
SELECT DISTINCT ON (instrument_id) *
FROM instrument_calibrations
WHERE passed
ORDER BY instrument_id, calibrated_on DESC, created_at DESC;
