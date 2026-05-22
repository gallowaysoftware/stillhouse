DROP INDEX IF EXISTS bottling_runs_active_idx;

ALTER TABLE bottling_runs
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS voided_by,
    DROP COLUMN IF EXISTS voided_reason;
