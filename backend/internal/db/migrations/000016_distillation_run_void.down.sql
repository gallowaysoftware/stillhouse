DROP INDEX IF EXISTS distillation_runs_active_idx;
ALTER TABLE distillation_runs
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS voided_by,
    DROP COLUMN IF EXISTS voided_reason;
