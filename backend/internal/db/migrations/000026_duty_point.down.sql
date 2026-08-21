DROP INDEX IF EXISTS bottling_runs_duty_idx;

ALTER TABLE bottling_runs
    DROP COLUMN IF EXISTS duty_rate_source,
    DROP COLUMN IF EXISTS duty_amount_cad,
    DROP COLUMN IF EXISTS duty_rate_per_laa;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS duty_point_effective_from,
    DROP COLUMN IF EXISTS duty_point;

DROP TYPE IF EXISTS duty_point;
