-- Mass in kilograms, or NULL for a unit that is not one. The same table as
-- internal/units; a test holds the two together.
CREATE FUNCTION kg_of(quantity DOUBLE PRECISION, uom TEXT) RETURNS DOUBLE PRECISION
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT quantity * CASE lower(btrim(uom))
        WHEN 'kg' THEN 1 WHEN 'kgs' THEN 1 WHEN 'kilo' THEN 1 WHEN 'kilos' THEN 1
        WHEN 'kilogram' THEN 1 WHEN 'kilograms' THEN 1
        WHEN 'g' THEN 0.001 WHEN 'gram' THEN 0.001 WHEN 'grams' THEN 0.001
        WHEN 't' THEN 1000 WHEN 'tonne' THEN 1000 WHEN 'tonnes' THEN 1000
        WHEN 'lb' THEN 0.45359237 WHEN 'lbs' THEN 0.45359237
        WHEN 'pound' THEN 0.45359237 WHEN 'pounds' THEN 0.45359237
    END
$$;

-- The bench's arithmetic, not a looser copy: grain and malt only, in
-- kilograms, every one with an extract figure, over a measured wash. The
-- old version read water as wash, multiplied grams as kilograms, and left
-- out grains with no extract while their sugar stayed in the gravity.
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
           SUM(kg_of(mu.quantity_used, mu.uom) * m.extract_fraction),
           MAX(og.value),
           MAX(wash.value),
           SUM(kg_of(mu.quantity_used, mu.uom))
    FROM mash_runs mr
    JOIN tenants t ON t.id = mr.tenant_id AND t.benchmark_opt_in
    JOIN mash_ingredient_usage mu ON mu.mash_run_id = mr.id
    JOIN materials m ON m.id = mu.material_id AND m.kind IN ('grain', 'malt')
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
    GROUP BY mr.tenant_id, mr.id
    HAVING bool_and(COALESCE(m.extract_fraction > 0 AND kg_of(mu.quantity_used, mu.uom) IS NOT NULL, FALSE))
       AND MAX(og.value) > 1.0
       AND MAX(wash.value) > 0;
$$;
ALTER FUNCTION bench_conversion_inputs() OWNER TO stillhouse_bench;
