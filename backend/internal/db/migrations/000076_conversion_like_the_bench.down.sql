-- Back to 000070's version.
CREATE OR REPLACE FUNCTION bench_conversion_inputs()
RETURNS TABLE (
    tenant_id UUID,
    mash_run_id UUID,
    extract_available_kg DOUBLE PRECISION,
    original_gravity DOUBLE PRECISION,
    wash_volume_l DOUBLE PRECISION,
    grain_kg DOUBLE PRECISION
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT mr.tenant_id,
           mr.id,
           SUM(mu.quantity_used * m.extract_fraction) FILTER (WHERE m.extract_fraction IS NOT NULL),
           MAX(og.value),
           MAX(COALESCE(wash.value, water.value)),
           SUM(mu.quantity_used) FILTER (WHERE m.extract_fraction IS NOT NULL)
    FROM mash_runs mr
    JOIN tenants t ON t.id = mr.tenant_id AND t.benchmark_opt_in
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
    GROUP BY mr.tenant_id, mr.id
    HAVING SUM(mu.quantity_used * m.extract_fraction) > 0
       AND MAX(og.value) > 1.0
       AND MAX(COALESCE(wash.value, water.value)) > 0;
$$;
ALTER FUNCTION bench_conversion_inputs() OWNER TO stillhouse_bench;

DROP FUNCTION kg_of(DOUBLE PRECISION, TEXT);
