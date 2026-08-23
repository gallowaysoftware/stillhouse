-- 000067_benchmarks: anonymised cross-tenant comparison. PLAN J2.
--
-- Every other table in this schema is protected by RLS: a tenant sees its
-- own rows and nothing else. This feature deliberately reaches across
-- that line, so it is the one place where getting the privacy wrong
-- publishes one distillery's operations to its competitors. The whole
-- migration is the guard rails.
--
-- Four of them, and each closes a different hole:
--
--   1. OPT-IN, off by default and never inferred. A distillery that has
--      not said yes contributes nothing and is not counted. Consent that
--      has to be withdrawn is not consent.
--
--   2. RECIPROCITY. Only a participant may read the benchmarks. A reader
--      who contributes nothing is a pure extractor, and the feature is a
--      network effect rather than a data tap.
--
--   3. A k-ANONYMITY FLOOR of distinct contributing TENANTS, not
--      observations. One distillery with four hundred casks is still one
--      distillery, and a cohort of one tenant's casks is that tenant's
--      own figure with a label on it.
--
--   4. DOMINANCE SUPPRESSION. Even at k tenants, if one supplies most of
--      the observations the cohort statistic sits on top of theirs. A
--      contributor over half the sample suppresses the figure.
--
-- And what is reported is percentiles, never extremes. A maximum IS one
-- participant's exact number; publishing it is publishing them.

ALTER TABLE tenants ADD COLUMN benchmark_opt_in BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE tenants ADD COLUMN benchmark_opt_in_at TIMESTAMPTZ;
ALTER TABLE tenants ADD COLUMN benchmark_opt_in_by UUID REFERENCES users(id);

COMMENT ON COLUMN tenants.benchmark_opt_in IS
    'Whether this licensee has agreed to contribute anonymised operational figures to the cross-tenant benchmarks, and thereby to read them. Off by default and never inferred.';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'stillhouse_bench') THEN
        CREATE ROLE stillhouse_bench NOLOGIN BYPASSRLS;
    END IF;
END $$;

GRANT SELECT ON tenants, bulk_containers, barrel_attributes, barrel_events,
                distillation_runs, distillation_cuts TO stillhouse_bench;

-- bench_angels_share: one row per cask, across every opted-in tenant.
--
-- Returns the tenant id alongside the value ON PURPOSE, and the caller
-- never returns it: it is what makes the k-count and the dominance check
-- possible at all. A function that returned only values could not tell
-- four hundred casks from four hundred distilleries.
CREATE OR REPLACE FUNCTION bench_angels_share()
RETURNS TABLE (tenant_id UUID, pct_per_year DOUBLE PRECISION, rickhouse TEXT)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT c.tenant_id,
           (fill.laa - c.current_laa) / fill.laa * 100
               / (GREATEST(CURRENT_DATE - ba.fill_date, 1) / 365.25),
           COALESCE(NULLIF(ba.rickhouse, ''), 'unstated')
    FROM bulk_containers c
    JOIN tenants t             ON t.id = c.tenant_id AND t.benchmark_opt_in
    JOIN barrel_attributes ba  ON ba.container_id = c.id
    JOIN LATERAL (
        SELECT e.laa
        FROM barrel_events e
        WHERE e.container_id = c.id AND e.kind = 'fill'
          AND e.voided_at IS NULL AND e.laa IS NOT NULL AND e.laa > 0
        ORDER BY e.event_date
        LIMIT 1
    ) fill ON TRUE
    WHERE c.kind = 'barrel'
      AND NOT c.archived
      AND ba.fill_date IS NOT NULL
      -- At least a season in wood. A cask filled last week has an annual
      -- rate that is arithmetic noise, and a benchmark full of noise is
      -- worse than no benchmark.
      AND CURRENT_DATE - ba.fill_date >= 90
      AND c.current_laa > 0;
$$;

-- bench_cut_ratio: hearts as a share of a run's total cut alcohol.
CREATE OR REPLACE FUNCTION bench_cut_ratio()
RETURNS TABLE (tenant_id UUID, hearts_pct DOUBLE PRECISION)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT r.tenant_id,
           SUM(cu.laa) FILTER (WHERE cu.kind = 'hearts') / NULLIF(SUM(cu.laa), 0) * 100
    FROM distillation_runs r
    JOIN tenants t        ON t.id = r.tenant_id AND t.benchmark_opt_in
    JOIN distillation_cuts cu ON cu.distillation_run_id = r.id
    WHERE r.voided_at IS NULL
    GROUP BY r.id, r.tenant_id
    HAVING SUM(cu.laa) > 0
       AND SUM(cu.laa) FILTER (WHERE cu.kind = 'hearts') IS NOT NULL;
$$;

ALTER FUNCTION bench_angels_share() OWNER TO stillhouse_bench;
ALTER FUNCTION bench_cut_ratio()    OWNER TO stillhouse_bench;

REVOKE ALL ON FUNCTION bench_angels_share() FROM PUBLIC;
REVOKE ALL ON FUNCTION bench_cut_ratio()    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION bench_angels_share() TO stillhouse_app;
GRANT EXECUTE ON FUNCTION bench_cut_ratio()    TO stillhouse_app;
