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

-- name: BenchmarkConversionInputs :many
-- Inputs, not answers: internal/mashing owns the arithmetic that turns
-- these into a conversion efficiency, and a second copy of it here would
-- drift from the CIBD-cited original. See 000070.
SELECT b.tenant_id::uuid AS tenant_id,
       b.extract_available_kg::double precision AS extract_available_kg,
       b.original_gravity::double precision AS original_gravity,
       b.wash_volume_l::double precision AS wash_volume_l,
       b.grain_kg::double precision AS grain_kg
FROM bench_conversion_inputs() b;

-- name: BenchmarkYieldPerTonne :many
SELECT b.tenant_id::uuid AS tenant_id,
       b.laa_per_tonne::double precision AS laa_per_tonne
FROM bench_yield_per_tonne() b;

-- name: MyConversionInputs :many
-- The caller's own, computed from the same columns so "you against the
-- cohort" compares like with like.
SELECT mr.id,
       SUM(mu.quantity_used * m.extract_fraction) FILTER (WHERE m.extract_fraction IS NOT NULL)::double precision AS extract_available_kg,
       MAX(og.value)::double precision AS original_gravity,
       MAX(COALESCE(wash.value, water.value))::double precision AS wash_volume_l
FROM mash_runs mr
JOIN mash_ingredient_usage mu ON mu.mash_run_id = mr.id
JOIN materials m ON m.id = mu.material_id
LEFT JOIN LATERAL (
    SELECT mm.value FROM mash_metrics mm
    WHERE mm.mash_run_id = mr.id AND mm.kind = 'original_gravity'
    ORDER BY mm.observed_at DESC LIMIT 1
) og ON TRUE
LEFT JOIN LATERAL (
    SELECT mm.value FROM mash_metrics mm
    WHERE mm.mash_run_id = mr.id AND mm.kind = 'wash_volume_l'
    ORDER BY mm.observed_at DESC LIMIT 1
) wash ON TRUE
LEFT JOIN LATERAL (
    SELECT mm.value FROM mash_metrics mm
    WHERE mm.mash_run_id = mr.id AND mm.kind = 'water_volume_l'
    ORDER BY mm.observed_at DESC LIMIT 1
) water ON TRUE
GROUP BY mr.id
HAVING SUM(mu.quantity_used * m.extract_fraction) > 0
   AND MAX(og.value) > 1.0
   AND MAX(COALESCE(wash.value, water.value)) > 0;
