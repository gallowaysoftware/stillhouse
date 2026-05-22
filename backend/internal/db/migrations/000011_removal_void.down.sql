DROP INDEX IF EXISTS packaging_removals_active_idx;

ALTER TABLE packaging_removals
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS voided_by,
    DROP COLUMN IF EXISTS voided_reason;
