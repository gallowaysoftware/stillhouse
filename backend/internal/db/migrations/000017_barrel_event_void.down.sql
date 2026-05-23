DROP INDEX IF EXISTS barrel_events_active_idx;
ALTER TABLE barrel_events
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS voided_by,
    DROP COLUMN IF EXISTS voided_reason;
