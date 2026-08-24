-- 000070_benchmark_metrics: two more figures for the cohort. PLAN J2.
--
-- Both go through the keyhole 000067 built, and both return INPUTS rather
-- than answers. That is deliberate and it is the same argument as the WIP
-- walk in 000061: the arithmetic that turns a mash into a conversion
-- efficiency already exists in internal/mashing, cited to the CIBD
-- curriculum, and a second copy of it in SQL would drift from the first.
-- What a benchmark needs is that every tenant's number came out of the
-- same code, which is exactly what a second copy prevents.
--
-- Yield per tonne carries a comparability constraint that is worth
-- stating rather than hiding. A distillation run charged from several
-- fermentations off several mashes has no unambiguous grain weight behind
-- it — apportioning one needs a convention, tenants may state different
-- ones (000061), and a cohort built from figures computed on different
-- conventions is comparing different things while looking like it is not.
-- So the function reports only gauges whose whole chain traces to a
-- SINGLE mash. Fewer rows, and every one of them means the same thing.

-- Inputs for conversion efficiency, per mash. Go does the arithmetic.
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

-- Yield per tonne, only where the chain is unambiguous.
CREATE OR REPLACE FUNCTION bench_yield_per_tonne()
RETURNS TABLE (tenant_id UUID, laa_per_tonne DOUBLE PRECISION)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    WITH chain AS (
        SELECT g.tenant_id,
               g.id AS gauge_id,
               g.laa,
               COUNT(DISTINCT fr.mash_run_id) AS mash_count,
               (ARRAY_AGG(DISTINCT fr.mash_run_id))[1] AS mash_run_id
        FROM production_gauges g
        JOIN tenants t             ON t.id = g.tenant_id AND t.benchmark_opt_in
        JOIN distillation_runs dr  ON dr.id = g.distillation_run_id AND dr.voided_at IS NULL
        JOIN distillation_charges dc ON dc.distillation_run_id = dr.id
        JOIN fermentation_runs fr  ON fr.id = dc.fermentation_run_id
        WHERE g.laa > 0
        GROUP BY g.tenant_id, g.id, g.laa
        -- One mash behind the gauge, so no apportionment convention is
        -- involved and every tenant's figure means the same thing.
        HAVING COUNT(DISTINCT fr.mash_run_id) = 1
    ), grain AS (
        SELECT mu.mash_run_id, SUM(mu.quantity_used) AS kg
        FROM mash_ingredient_usage mu
        JOIN materials m ON m.id = mu.material_id
        -- Kilogrammes of fermentable only. A botanical measured in grams
        -- is not part of a yield-per-tonne figure.
        WHERE m.extract_fraction IS NOT NULL AND mu.uom = 'kg'
        GROUP BY mu.mash_run_id
    )
    SELECT c.tenant_id, c.laa / (grain.kg / 1000.0)
    FROM chain c
    JOIN grain ON grain.mash_run_id = c.mash_run_id
    WHERE grain.kg > 0;
$$;

ALTER FUNCTION bench_conversion_inputs() OWNER TO stillhouse_bench;
ALTER FUNCTION bench_yield_per_tonne()   OWNER TO stillhouse_bench;

REVOKE ALL ON FUNCTION bench_conversion_inputs() FROM PUBLIC;
REVOKE ALL ON FUNCTION bench_yield_per_tonne()   FROM PUBLIC;
GRANT EXECUTE ON FUNCTION bench_conversion_inputs() TO stillhouse_app;
GRANT EXECUTE ON FUNCTION bench_yield_per_tonne()   TO stillhouse_app;

-- The yield walk joins charges as well as runs; 000067 granted the runs
-- and not the charges, which SELECT-denied the whole function rather than
-- returning a partial answer. The keyhole failing closed is the right
-- behaviour and the grant is still the fix.
GRANT SELECT ON mash_runs, mash_ingredient_usage, mash_metrics, materials,
                production_gauges, fermentation_runs, distillation_charges
                TO stillhouse_bench;
