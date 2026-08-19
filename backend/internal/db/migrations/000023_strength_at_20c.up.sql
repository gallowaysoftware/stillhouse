-- Strength and volume at 20 °C, per the CRA Canadian Alcoholometric
-- Tables 1980.
--
-- Alcoholic strength only means something at a reference temperature, and
-- CRA's is 20 °C. Until now Stillhouse stored whatever the operator typed
-- and treated it as if it were already at 20 °C — so a receiver gauged
-- warm was filed with both its strength and its volume overstated.
--
-- The fix keeps volume_l / abv_pct as the values AT 20 °C (the generated
-- laa column already depends on them, and they are the legally meaningful
-- pair), and records the as-observed reading alongside so any gauge can be
-- audited back to what the operator actually saw on the instrument.
--
-- see also stage 109 (bottling conserves LAA)

-- How the 20 °C strength was arrived at.
--   uncorrected    — no temperature recorded; the figure is taken as given.
--                    Every row that predates this migration is this.
--   table_density  — hydrometer indication (kg/m³) + temperature, resolved
--                    through the published tables. CRA's approved path.
--   table_strength — strength already expressed at 20 °C by the instrument
--                    (e.g. a density meter), with the tables supplying only
--                    the volume factor for the measurement temperature.
CREATE TYPE strength_source AS ENUM ('uncorrected', 'table_density', 'table_strength');

ALTER TABLE production_gauges
    ADD COLUMN observed_volume_l      DOUBLE PRECISION,
    ADD COLUMN observed_density_kg_m3 DOUBLE PRECISION,
    ADD COLUMN volume_factor_c        DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    ADD COLUMN strength_source        strength_source NOT NULL DEFAULT 'uncorrected';

UPDATE production_gauges SET observed_volume_l = volume_l WHERE observed_volume_l IS NULL;

ALTER TABLE barrel_events
    ADD COLUMN temperature_c          DOUBLE PRECISION,
    ADD COLUMN observed_volume_l      DOUBLE PRECISION,
    ADD COLUMN observed_density_kg_m3 DOUBLE PRECISION,
    ADD COLUMN volume_factor_c        DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    ADD COLUMN strength_source        strength_source NOT NULL DEFAULT 'uncorrected';

UPDATE barrel_events SET observed_volume_l = volume_l WHERE observed_volume_l IS NULL;
