-- name: GetBenchmarkOptIn :one
SELECT benchmark_opt_in, benchmark_opt_in_at FROM tenants WHERE id = $1;

-- name: SetBenchmarkOptIn :one
-- Opting in stamps who and when; opting out clears them, so the record
-- never says somebody consented when they have since withdrawn.
UPDATE tenants
SET benchmark_opt_in    = @opt_in::boolean,
    benchmark_opt_in_at = CASE WHEN @opt_in::boolean THEN NOW() ELSE NULL END,
    benchmark_opt_in_by = CASE WHEN @opt_in::boolean THEN @user_id::uuid ELSE NULL END,
    updated_at          = NOW()
WHERE id = @id
RETURNING benchmark_opt_in, benchmark_opt_in_at;

-- name: BenchmarkAngelsShare :many
-- Through the keyhole; see 000067. Opted-in tenants only, enforced in the
-- function rather than here.
SELECT b.tenant_id::uuid AS tenant_id,
       b.pct_per_year::double precision AS pct_per_year,
       b.rickhouse::text AS rickhouse
FROM bench_angels_share() b;

-- name: BenchmarkCutRatio :many
SELECT b.tenant_id::uuid AS tenant_id,
       b.hearts_pct::double precision AS hearts_pct
FROM bench_cut_ratio() b;

-- name: MyAngelsShare :one
-- The caller's own figure, computed the same way, so "you against the
-- cohort" is a comparison rather than two different measurements.
SELECT COALESCE(AVG(
    (fill.laa - c.current_laa) / fill.laa * 100
        / (GREATEST(CURRENT_DATE - ba.fill_date, 1) / 365.25)
), 0)::double precision AS pct_per_year,
COUNT(*)::int AS casks
FROM bulk_containers c
JOIN barrel_attributes ba ON ba.container_id = c.id
JOIN LATERAL (
    SELECT e.laa FROM barrel_events e
    WHERE e.container_id = c.id AND e.kind = 'fill'
      AND e.voided_at IS NULL AND e.laa IS NOT NULL AND e.laa > 0
    ORDER BY e.event_date LIMIT 1
) fill ON TRUE
WHERE c.kind = 'barrel' AND NOT c.archived
  AND ba.fill_date IS NOT NULL
  AND CURRENT_DATE - ba.fill_date >= 90
  AND c.current_laa > 0;
