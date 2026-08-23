ALTER TABLE recipe_versions
    DROP CONSTRAINT IF EXISTS recipe_versions_mash_efficiency_fraction_check,
    DROP CONSTRAINT IF EXISTS recipe_versions_ferment_efficiency_fraction_check,
    DROP CONSTRAINT IF EXISTS recipe_versions_distillation_recovery_fraction_check;

ALTER TABLE recipe_versions RENAME COLUMN distillation_recovery_fraction TO distillation_recovery_pct;
ALTER TABLE recipe_versions RENAME COLUMN ferment_efficiency_fraction    TO ferment_efficiency_pct;
ALTER TABLE recipe_versions RENAME COLUMN mash_efficiency_fraction       TO mash_efficiency_pct;

ALTER TABLE recipe_versions
    ADD CONSTRAINT recipe_versions_mash_efficiency_pct_check
        CHECK (mash_efficiency_pct > 0 AND mash_efficiency_pct <= 1),
    ADD CONSTRAINT recipe_versions_ferment_efficiency_pct_check
        CHECK (ferment_efficiency_pct > 0 AND ferment_efficiency_pct <= 1),
    ADD CONSTRAINT recipe_versions_distillation_recovery_pct_check
        CHECK (distillation_recovery_pct > 0 AND distillation_recovery_pct <= 1);

ALTER TABLE materials RENAME COLUMN moisture_fraction TO moisture_pct;
ALTER TABLE materials RENAME COLUMN extract_fraction  TO extract_pct;
