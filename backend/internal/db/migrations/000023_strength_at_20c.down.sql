ALTER TABLE barrel_events
    DROP COLUMN strength_source,
    DROP COLUMN volume_factor_c,
    DROP COLUMN observed_density_kg_m3,
    DROP COLUMN observed_volume_l,
    DROP COLUMN temperature_c;

ALTER TABLE production_gauges
    DROP COLUMN strength_source,
    DROP COLUMN volume_factor_c,
    DROP COLUMN observed_density_kg_m3,
    DROP COLUMN observed_volume_l;

DROP TYPE strength_source;
