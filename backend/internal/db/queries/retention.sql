-- name: GetRetentionPolicy :one
SELECT * FROM retention_policies WHERE tenant_id = $1;

-- name: SaveRetentionPolicy :one
INSERT INTO retention_policies (
    tenant_id, retention_years, backup_cadence, restore_notes,
    reviewed_on, reviewed_by, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id) DO UPDATE
SET retention_years = EXCLUDED.retention_years,
    backup_cadence  = EXCLUDED.backup_cadence,
    restore_notes   = EXCLUDED.restore_notes,
    reviewed_on     = EXCLUDED.reviewed_on,
    reviewed_by     = EXCLUDED.reviewed_by,
    notes           = EXCLUDED.notes,
    updated_at      = NOW()
RETURNING *;

-- name: PlaceLegalHold :one
INSERT INTO legal_holds (
    tenant_id, reason, instructed_by, reference, placed_on, placed_by
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ReleaseLegalHold :one
UPDATE legal_holds
SET released_on = $2, released_by = $3, release_reason = $4
WHERE id = $1 AND released_on IS NULL
RETURNING *;

-- name: ListLegalHolds :many
SELECT h.*, COALESCE(p.display_name, '') AS placed_by_name,
       COALESCE(r.display_name, '') AS released_by_name
FROM legal_holds h
LEFT JOIN users p ON p.id = h.placed_by
LEFT JOIN users r ON r.id = h.released_by
ORDER BY h.released_on NULLS FIRST, h.placed_on DESC;

-- name: OpenLegalHoldCount :one
-- Checked before any path that really removes a row. One query, in one
-- place, so a delete added later cannot quietly escape a hold.
SELECT COUNT(*)::int AS n FROM legal_holds WHERE released_on IS NULL;

-- name: RetentionCoverage :many
-- The oldest record Stillhouse still holds in each class it would be
-- asked for, and how many there are.
--
-- Deliberately a fixed list rather than a walk over every table: what
-- s.206(1) asks for is records sufficient to determine compliance, and
-- that is the production and duty chain, not the session store. A class
-- with no rows reports a NULL date rather than today, because "we have
-- nothing" and "our oldest is from this morning" are different answers.
SELECT 'Bulk movements'    AS record_class, MIN(occurred_at)::date AS oldest, COUNT(*)::int AS rows FROM bulk_movements
UNION ALL SELECT 'Production gauges',  MIN(created_at)::date,  COUNT(*)::int FROM production_gauges
UNION ALL SELECT 'Mash runs',          MIN(created_at)::date,  COUNT(*)::int FROM mash_runs
UNION ALL SELECT 'Distillation runs',  MIN(created_at)::date,  COUNT(*)::int FROM distillation_runs
UNION ALL SELECT 'Barrel events',      MIN(event_date)::date,  COUNT(*)::int FROM barrel_events
UNION ALL SELECT 'Bottling runs',      MIN(bottling_date)::date, COUNT(*)::int FROM bottling_runs
UNION ALL SELECT 'Removals',           MIN(removal_date)::date, COUNT(*)::int FROM packaging_removals
UNION ALL SELECT 'Excise stamp orders', MIN(ordered_at)::date, COUNT(*)::int FROM excise_stamp_orders
UNION ALL SELECT 'B266 periods',       MIN(period_start)::date, COUNT(*)::int FROM b266_periods
UNION ALL SELECT 'Inventory adjustments', MIN(occurred_at)::date, COUNT(*)::int FROM inventory_adjustments
UNION ALL SELECT 'Material receipts',  MIN(received_at)::date, COUNT(*)::int FROM material_lots
UNION ALL SELECT 'Audit events',       MIN(occurred_at)::date, COUNT(*)::int FROM audit_events
ORDER BY record_class;
