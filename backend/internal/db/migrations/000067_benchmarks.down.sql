DROP FUNCTION IF EXISTS bench_cut_ratio();
DROP FUNCTION IF EXISTS bench_angels_share();
ALTER TABLE tenants DROP COLUMN IF EXISTS benchmark_opt_in_by;
ALTER TABLE tenants DROP COLUMN IF EXISTS benchmark_opt_in_at;
ALTER TABLE tenants DROP COLUMN IF EXISTS benchmark_opt_in;
-- stillhouse_bench is left, as stillhouse_auth and stillhouse_webhook are:
-- a NOLOGIN role owning nothing grants nothing.
